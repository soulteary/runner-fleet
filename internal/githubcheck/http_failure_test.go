package githubcheck

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAPIResponseAndPaginationFailures(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		fakeAPI(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = fmt.Fprint(w, "{") })
		var out map[string]any
		if err := doJSON(context.Background(), http.DefaultClient, http.MethodGet, apiBase, tokenRef{}, &out); err == nil {
			t.Fatalf("malformed response %d accepted", status)
		}
		if status == http.StatusOK {
			if err := doJSON(context.Background(), http.DefaultClient, http.MethodGet, apiBase, tokenRef{}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := doJSON(context.Background(), http.DefaultClient, http.MethodGet, ":invalid", tokenRef{}, nil); err == nil {
		t.Fatal("invalid URL accepted")
	}
	if _, err := runnersEndpoint("repo", "not-a-repository"); err == nil {
		t.Fatal("invalid repository endpoint accepted")
	}
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(maxPages*apiPerPage+1, strings.Split(strings.Repeat("alpha,", apiPerPage), ",")[:apiPerPage]...))
	})
	list, err := listRunners(context.Background(), http.DefaultClient, tokenRef{}, "repo", "o/r")
	if err == nil || len(list) != maxPages*apiPerPage || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("pagination cap silently accepted: len=%d err=%v", len(list), err)
	}
	if !visibilityDue("repo", "bad timestamp", time.Now()) {
		t.Fatal("invalid timestamp suppressed visibility refresh")
	}
	resp := &http.Response{StatusCode: 403, Header: http.Header{"X-Ratelimit-Remaining": []string{"0"}, "X-Ratelimit-Reset": []string{fmt.Sprint(time.Now().Add(time.Minute).Unix())}}}
	if err := httpError(resp, tokenRef{}); err == nil || !strings.Contains(err.Error(), "resets in") {
		t.Fatalf("missing reset diagnostic: %v", err)
	}
	if !errors.Is(httpError(&http.Response{StatusCode: 404}, tokenRef{}), errNotFound) {
		t.Fatal("404 sentinel identity lost")
	}
}
