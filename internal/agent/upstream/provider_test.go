package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/agent"
	"github.com/contember/edvabe/internal/runtime/noop"
)

func TestProviderMetadata(t *testing.T) {
	p := New()
	if got := p.Name(); got != "upstream-envd" {
		t.Errorf("Name = %q", got)
	}
	if got := p.Version(); got != DefaultEnvdVersion {
		t.Errorf("Version = %q", got)
	}
	if got := p.Port(); got != 49983 {
		t.Errorf("Port = %d", got)
	}
}

func TestEnsureImageRejectsEmptyTag(t *testing.T) {
	p := New()
	if err := p.EnsureImage(context.Background(), noop.New(), ""); err == nil {
		t.Fatal("EnsureImage should fail for empty tag")
	}
}

func TestPingSucceedsOn204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := New()
	p.pingWindow = 500 * time.Millisecond
	if err := p.Ping(context.Background(), srv.URL); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestInitAgentPostsExpectedPayload(t *testing.T) {
	var got initRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/init" {
			t.Fatalf("path = %q, want /init", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := New()
	err := p.InitAgent(context.Background(), srv.URL, agent.InitConfig{
		AccessToken:    "ea_test",
		EnvVars:        map[string]string{"FOO": "bar"},
		DefaultUser:    "user",
		DefaultWorkdir: "/home/user",
		VolumeMounts:   []agent.VolumeMount{{Name: "vol", MountPath: "/volumes/vol"}},
	})
	if err != nil {
		t.Fatalf("InitAgent: %v", err)
	}
	if got.AccessToken != "ea_test" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
	if got.EnvVars["FOO"] != "bar" {
		t.Errorf("EnvVars = %#v", got.EnvVars)
	}
	if got.DefaultUser != "user" {
		t.Errorf("DefaultUser = %q", got.DefaultUser)
	}
	if got.DefaultWorkdir != "/home/user" {
		t.Errorf("DefaultWorkdir = %q", got.DefaultWorkdir)
	}
	if len(got.VolumeMounts) != 1 || got.VolumeMounts[0].MountPath != "/volumes/vol" {
		t.Errorf("VolumeMounts = %#v", got.VolumeMounts)
	}
	if got.Timestamp == "" {
		t.Error("Timestamp is empty")
	}
}
