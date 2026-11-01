package githubcheck

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestDeregisterDoesNotTreatTransportErrorsAs404(t *testing.T) {
	for _, tc := range []struct {
		target string
		id     int64
	}{{"o/r", 404}, {"o404/r", 100}} {
		t.Run(fmt.Sprintf("%s/%d", tc.target, tc.id), func(t *testing.T) {
			dir, store := withToken(t, "fixture-pat")
			var deleteReceived atomic.Bool
			fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deleteReceived.Store(true)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				_, _ = fmt.Fprintf(w, `{"total_count":1,"runners":[{"id":%d,"name":"alpha"}]}`, tc.id)
			})
			result := Deregister(context.Background(), store, dir, "repo", tc.target, "alpha")
			if !deleteReceived.Load() {
				t.Fatal("DELETE was not attempted")
			}
			if result.Done || result.MessageKey != "github.dereg.delete_failed" {
				t.Fatalf("lost acknowledgement reported as confirmed absence: %+v", result)
			}
			if store.GitHubToken("alpha") != "fixture-pat" {
				t.Fatal("uncertain deletion changed credentials")
			}
		})
	}
}
