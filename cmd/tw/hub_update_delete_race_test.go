package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	var out map[string]any
	if len(w.Body.Bytes()) > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("response is not JSON: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

func putProjectUpdate(t *testing.T, h *hub, id, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/projects/"+id, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.serveProjectItem(w, r)
	return decodeResponse(t, w)
}

func deleteProjectRequest(t *testing.T, h *hub, id string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/api/projects/"+id+"?confirm="+id, nil)
	w := httptest.NewRecorder()
	h.serveProjectItem(w, r)
	return decodeResponse(t, w)
}

// A settings write inside the delete window must fail closed with 409, not
// persist into a tree the delete is removing.
func TestUpdateProjectConflictsDuringDeleteWindow(t *testing.T) {
	h, _ := createOptionsHub(t, hubConfig{NoDecider: true})
	code, out := postProject(t, h, `{"name":"Acme","scope":"acme.example","goal":"拿到边界主机"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, out)
	}
	id := out["id"].(string)
	if k := h.kits[id]; k == nil {
		t.Fatal("create did not attach the project")
	} else {
		k.srv.SetDeleting(true)
	}
	code, out = putProjectUpdate(t, h, id, `{"max_runs":2}`)
	if code != http.StatusConflict || out["code"] != "project_deleting" {
		t.Fatalf("update during delete = %d %v; want 409 project_deleting", code, out)
	}
}

// updateProject must not panic on a project that disappears mid-request, and a
// concurrent update/delete pair must end with the project gone and every
// update answered with a definite status (200 / 404 / 409 / 410), never a 500.
func TestUpdateProjectRacesDeleteWithoutPanic(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
		code, out := postProject(t, h, `{"name":"Acme","scope":"acme.example","goal":"拿到边界主机","max_runs":1}`)
		if code != http.StatusCreated {
			t.Fatalf("create = %d %v", code, out)
		}
		id := out["id"].(string)

		var wg sync.WaitGroup
		stop := make(chan struct{})
		statuses := make(chan int, 512)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				code, out := putProjectUpdate(t, h, id, `{"max_runs":2}`)
				statuses <- code
				_ = out
			}
		}()
		// Give the updater a beat so it is inside updateProject when the delete
		// seals the project, then remove it through the handler.
		time.Sleep(2 * time.Millisecond)
		dCode, dOut := deleteProjectRequest(t, h, id)
		close(stop)
		wg.Wait()
		close(statuses)
		// A settings write racing the RemoveAll can legitimately make the delete
		// report "directory not empty" and take its rebuild fallback (500): the
		// handler must answer with a definite status either way, never hang or
		// panic. The subject here is the update path's behaviour.
		switch dCode {
		case http.StatusOK:
			if mgr.Get(id) != nil {
				t.Fatalf("project %s survived a reported delete", id)
			}
		case http.StatusInternalServerError:
			if !strings.Contains(fmt.Sprint(dOut["error"]), "目录没删干净") {
				t.Fatalf("delete = %d %v; want success or the known rebuild fallback", dCode, dOut)
			}
		default:
			t.Fatalf("delete = %d %v", dCode, dOut)
		}
		for code := range statuses {
			switch code {
			case http.StatusOK, http.StatusNotFound, http.StatusConflict, http.StatusGone:
			default:
				t.Fatalf("concurrent update answered %d; want 200/404/409/410", code)
			}
		}
	}
}
