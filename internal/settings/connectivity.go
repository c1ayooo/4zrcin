package settings

import (
	"context"
	"strings"
	"time"

	"github.com/kksty/tianwu/internal/modelclient"
)

type ModelProbe struct {
	Probed        bool   `json:"probed"`
	Available     bool   `json:"available"`
	Error         string `json:"error,omitempty"`
	ReturnedModel string `json:"returned_model,omitempty"`
	MS            int64  `json:"ms"`
}

func (s *Store) CheckModel(ctx context.Context) ModelProbe {
	m := s.Effective()
	c, err := modelclient.NewWithProvider(m.BaseURL, m.APIKey, m.Model, m.Provider)
	if err != nil {
		return ModelProbe{Error: err.Error()}
	}
	c.MaxTokens = 256
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	started := time.Now()
	result, err := c.Check(ctx)
	out := ModelProbe{Probed: true, MS: time.Since(started).Milliseconds()}
	if err != nil {
		out.Error = err.Error()
		for _, secret := range s.SecretValues() {
			if secret != "" {
				out.Error = strings.ReplaceAll(out.Error, secret, "***")
			}
		}
	} else {
		out.Available = true
		out.ReturnedModel = result.ReturnedModel
	}
	return out
}
