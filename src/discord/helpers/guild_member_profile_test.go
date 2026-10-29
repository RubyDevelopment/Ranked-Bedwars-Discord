package helpers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// A profile change must name only the fields that change: Discord clears every field it receives as null, so sending the
// banner alone used to wipe the avatar and the about text.
func TestUpdateGuildMemberProfileSendsOnlyTheChangedFields(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	reject := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if _, bad := body[reject]; bad && reject != "" {
			http.Error(w, `{"message":"invalid image"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	api := NewDiscordRestAPI("token")
	api.baseURL = server.URL

	done, failures := api.UpdateGuildMemberProfile("1", map[string]any{"banner": "data:image/png;base64,AAAA"})
	if len(bodies) != 1 || len(bodies[0]) != 1 || bodies[0]["banner"] == nil {
		t.Fatalf("a banner change must send the banner and nothing else, sent %v", bodies)
	}
	if !reflect.DeepEqual(done, []string{"banner"}) || len(failures) != 0 {
		t.Errorf("done=%v failures=%v", done, failures)
	}

	bodies = nil
	done, failures = api.UpdateGuildMemberProfile("1", map[string]any{"nick": "Ranked", "avatar": nil, "bio": "hello"})
	if len(bodies) != 1 {
		t.Fatalf("several fields must go in one request, got %d", len(bodies))
	}
	if v, present := bodies[0]["avatar"]; !present || v != nil {
		t.Errorf("a nil value must be sent as null so it clears the field, got %v (present=%v)", v, present)
	}
	sort.Strings(done)
	if !reflect.DeepEqual(done, []string{"avatar", "bio", "nick"}) || len(failures) != 0 {
		t.Errorf("done=%v failures=%v", done, failures)
	}

	// One field refused: the others still land, each on its own.
	bodies, reject = nil, "banner"
	done, failures = api.UpdateGuildMemberProfile("1", map[string]any{"banner": "x", "bio": "hello"})
	if !reflect.DeepEqual(done, []string{"bio"}) || failures["banner"] == nil {
		t.Errorf("the bio should still be applied when the banner is refused: done=%v failures=%v", done, failures)
	}
}
