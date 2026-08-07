package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/render"
)

func TestRunPortUpdateRejectsAuthoritativeOverrideDriftWithoutPUT(t *testing.T) {
	const statDevice = `[{"_id":"sw1","mac":"aa:bb:cc:dd:ee:03","name":"Switch-Core","type":"usw","port_table":[{"port_idx":12,"name":"Port 12","media":"GE","speed":1000,"poe_mode":"auto","enable":true,"portconf_id":"prof-all"}],"port_overrides":[{"port_idx":12,"name":"AP-Uplink","poe_mode":"pasv24","portconf_id":"prof-ap"}]}]`
	const initialRestDevice = `[{"_id":"sw1","name":"Switch-Core","port_overrides":[{"port_idx":12,"name":"AP-Uplink","poe_mode":"pasv24","portconf_id":"prof-ap"}]}]`
	const driftedRestDevice = `[{"_id":"sw1","name":"Switch-Core","port_overrides":[{"port_idx":12,"name":"Changed Elsewhere","poe_mode":"auto","portconf_id":"prof-other"}]}]`

	var mu sync.Mutex
	restGets := 0
	puts := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != commandTestAPIKey {
			t.Errorf("X-API-KEY = %q", got)
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/stat/device":
			_, _ = io.WriteString(w, `{"data":`+statDevice+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/rest/device/sw1":
			mu.Lock()
			restGets++
			getNumber := restGets
			mu.Unlock()
			body := initialRestDevice
			if getNumber > 1 {
				body = driftedRestDevice
			}
			_, _ = io.WriteString(w, `{"data":`+body+`}`)
		case r.Method == http.MethodPut && r.URL.Path == "/proxy/network/api/s/default/rest/device/sw1":
			mu.Lock()
			puts++
			mu.Unlock()
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	useCommandTestRuntime(t, srv, true)
	flagYes = true
	stdout, stderr, err := captureProcessOutput(t, func() error {
		return runPortUpdate("Switch-Core", 12, domain.PortInput{POE: "off", SetPOE: true})
	})
	if err == nil {
		t.Fatalf("authoritative drift was applied; stdout=%q stderr=%q", stdout, stderr)
	}
	var env render.Envelope
	if decodeErr := json.Unmarshal([]byte(stdout), &env); decodeErr != nil {
		t.Fatalf("decode conflict envelope: %v; stdout=%q", decodeErr, stdout)
	}
	if env.Error == nil || env.Error.Code != string(apperr.Conflict) {
		t.Fatalf("error = %+v, want conflict; stdout=%q", env.Error, stdout)
	}
	mu.Lock()
	gotRestGets, gotPUTs := restGets, puts
	mu.Unlock()
	if gotRestGets < 2 {
		t.Fatalf("authoritative REST observations = %d, want preparation and revalidation", gotRestGets)
	}
	if gotPUTs != 0 {
		t.Fatalf("PUT requests = %d, want 0 after authoritative drift", gotPUTs)
	}
}
