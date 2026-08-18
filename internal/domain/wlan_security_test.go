package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

const radiusProfileID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestOfficialWlanCreateBuildsEveryApprovedSecurityVariant(t *testing.T) {
	tests := []struct {
		name         string
		in           domain.WlanInput
		wantSecurity map[string]any
	}{
		{
			name: "WPA3 personal",
			in: domain.WlanInput{Name: "Lab", Security: "wpa3-personal", Password: "password",
				SAEAnticloggingThresholdSeconds: 5, SetSAEAnticloggingThresholdSeconds: true,
				SAESyncTimeSeconds: 10, SetSAESyncTimeSeconds: true,
				FastRoamingEnabled: true, SetFastRoamingEnabled: true},
			wantSecurity: map[string]any{"type": "WPA3_PERSONAL", "passphrase": "password", "fastRoamingEnabled": true,
				"saeConfiguration": map[string]any{"anticloggingThresholdSeconds": 5, "syncTimeSeconds": 10}},
		},
		{
			name: "WPA2 WPA3 personal",
			in: domain.WlanInput{Name: "Lab", Security: "wpa2-wpa3-personal", Password: "password",
				PMFMode: "required", SetPMFMode: true,
				SAEAnticloggingThresholdSeconds: 5, SetSAEAnticloggingThresholdSeconds: true,
				SAESyncTimeSeconds: 10, SetSAESyncTimeSeconds: true,
				FastRoamingEnabled: true, SetFastRoamingEnabled: true,
				WPA3FastRoamingEnabled: false, SetWPA3FastRoamingEnabled: true},
			wantSecurity: map[string]any{"type": "WPA2_WPA3_PERSONAL", "passphrase": "password", "pmfMode": "REQUIRED",
				"fastRoamingEnabled": true, "wpa3FastRoamingEnabled": false,
				"saeConfiguration": map[string]any{"anticloggingThresholdSeconds": 5, "syncTimeSeconds": 10}},
		},
		{
			name: "WPA2 enterprise",
			in: domain.WlanInput{Name: "Lab", Security: "wpa2-enterprise",
				RadiusProfileID: radiusProfileID, SetRadiusProfileID: true,
				RadiusNASIDSource: "device-name", SetRadiusNASIDSource: true,
				COAEnabled: false, SetCOAEnabled: true,
				PMFMode: "optional", SetPMFMode: true},
			wantSecurity: map[string]any{"type": "WPA2_ENTERPRISE", "coaEnabled": false, "pmfMode": "OPTIONAL",
				"radiusConfiguration": map[string]any{"profileId": radiusProfileID, "nasId": map[string]any{"type": "DERIVED", "source": "DEVICE_NAME"}}},
		},
		{
			name: "WPA2 WPA3 enterprise",
			in: domain.WlanInput{Name: "Lab", Security: "wpa2-wpa3-enterprise",
				RadiusProfileID: radiusProfileID, SetRadiusProfileID: true,
				RadiusNASID: "lab-ap", SetRadiusNASID: true,
				COAEnabled: true, SetCOAEnabled: true,
				PMFMode: "required", SetPMFMode: true,
				FastRoamingEnabled: true, SetFastRoamingEnabled: true,
				WPA3FastRoamingEnabled: true, SetWPA3FastRoamingEnabled: true},
			wantSecurity: map[string]any{"type": "WPA2_WPA3_ENTERPRISE", "coaEnabled": true, "pmfMode": "REQUIRED",
				"fastRoamingEnabled": true, "wpa3FastRoamingEnabled": true,
				"radiusConfiguration": map[string]any{"profileId": radiusProfileID, "nasId": map[string]any{"type": "USER_DEFINED", "value": "lab-ap"}}},
		},
		{
			name: "WPA3 enterprise",
			in: domain.WlanInput{Name: "Lab", Security: "wpa3-enterprise",
				RadiusProfileID: radiusProfileID, SetRadiusProfileID: true,
				RadiusNASIDSource: "bssid", SetRadiusNASIDSource: true,
				COAEnabled: false, SetCOAEnabled: true,
				WPA3SecurityMode: "high-security-192-bit", SetWPA3SecurityMode: true},
			wantSecurity: map[string]any{"type": "WPA3_ENTERPRISE", "coaEnabled": false, "securityMode": "HIGH_SECURITY_192_BIT",
				"radiusConfiguration": map[string]any{"profileId": radiusProfileID, "nasId": map[string]any{"type": "DERIVED", "source": "BSSID"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := wlanMutationAPI()
			createdID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
			path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", createdID)
			api.mutate = func(method, gotPath string, in, out any) error {
				observed := cloneMutationTestValue(in).(map[string]any)
				observed["id"] = createdID
				api.details[path] = observed
				return copyTestJSON(observed, out)
			}
			if _, err := domain.NewWlanService(api).ApplyCreate(context.Background(), tt.in); err != nil {
				t.Fatal(err)
			}
			posts := mutationCalls(api.official, http.MethodPost)
			if len(posts) != 1 {
				t.Fatalf("POSTs = %#v", posts)
			}
			body := posts[0].body.(map[string]any)
			if !reflect.DeepEqual(body["securityConfiguration"], cloneMutationTestValue(tt.wantSecurity)) {
				t.Fatalf("security body = %#v\nwant = %#v", body["securityConfiguration"], tt.wantSecurity)
			}
			if body["channel2gLockedTo6"] != false || body["dtimPeriod2gLockedTo3"] != false {
				t.Fatalf("10.4.57 required broadcast defaults missing: %#v", body)
			}
		})
	}
}

func TestOfficialWlanSecurityVariantsRejectIncompleteOrConflictingInputs(t *testing.T) {
	tests := []domain.WlanInput{
		{Name: "Lab", Security: "wpa3-personal", Password: "password"},
		{Name: "Lab", Security: "wpa2-wpa3-personal", Password: "password", SAEAnticloggingThresholdSeconds: 5, SetSAEAnticloggingThresholdSeconds: true, SAESyncTimeSeconds: 5, SetSAESyncTimeSeconds: true},
		{Name: "Lab", Security: "wpa2-enterprise", RadiusProfileID: radiusProfileID, SetRadiusProfileID: true, COAEnabled: true, SetCOAEnabled: true},
		{Name: "Lab", Security: "wpa3-enterprise", Password: "password", RadiusProfileID: radiusProfileID, SetRadiusProfileID: true, RadiusNASID: "x", SetRadiusNASID: true, COAEnabled: true, SetCOAEnabled: true, WPA3SecurityMode: "default", SetWPA3SecurityMode: true},
		{Name: "Lab", Security: "wpa2-enterprise", RadiusProfileID: radiusProfileID, SetRadiusProfileID: true, RadiusNASID: "x", SetRadiusNASID: true, RadiusNASIDSource: "bssid", SetRadiusNASIDSource: true, COAEnabled: true, SetCOAEnabled: true},
		{Name: "Lab", Security: "wpa3-personal", Password: "password", SAEAnticloggingThresholdSeconds: 5, SetSAEAnticloggingThresholdSeconds: true, SAESyncTimeSeconds: 5, SetSAESyncTimeSeconds: true, PMFMode: "required", SetPMFMode: true},
		{Name: "Lab", Security: "wpa3-personal", Password: "password", SAEAnticloggingThresholdSeconds: 5, SetSAEAnticloggingThresholdSeconds: true, SAESyncTimeSeconds: 5, SetSAESyncTimeSeconds: true, WPA3FastRoamingEnabled: false, SetWPA3FastRoamingEnabled: true},
	}
	for index, in := range tests {
		api := wlanMutationAPI()
		_, err := domain.NewWlanService(api).ApplyCreate(context.Background(), in)
		if !apperr.Is(err, apperr.ValidationFailed) {
			t.Errorf("case %d error = %v", index, err)
		}
		if got := len(nonGetMutationCalls(api.official)); got != 0 {
			t.Errorf("case %d writes = %d", index, got)
		}
	}
}

func TestOfficialWlanAdvancedSecurityUpdatePreservesDocumentAndPlanState(t *testing.T) {
	api := wlanMutationAPI()
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", id)
	api.mutate = func(method, gotPath string, in, out any) error {
		observed := cloneMutationTestValue(in).(map[string]any)
		observed["id"] = id
		observed["metadata"] = map[string]any{"origin": "USER"}
		api.details[path] = observed
		return copyTestJSON(observed, out)
	}
	in := domain.WlanInput{PMFMode: "optional", SetPMFMode: true, FastRoamingEnabled: false, SetFastRoamingEnabled: true}
	svc := domain.NewWlanService(api)
	p, _, err := svc.Update(context.Background(), id, in)
	if err != nil {
		t.Fatal(err)
	}
	after := p.Changes[0].After.(map[string]any)
	if after["pmf_mode"] != "optional" || after["fast_roaming_enabled"] != false {
		t.Fatalf("advanced plan = %#v", after)
	}
	if _, leaked := after["passphrase"]; leaked {
		t.Fatalf("plan leaked passphrase: %#v", after)
	}
	if _, err := svc.ApplyUpdate(context.Background(), id, in); err != nil {
		t.Fatal(err)
	}
	puts := mutationCalls(api.official, http.MethodPut)
	security := puts[0].body.(map[string]any)["securityConfiguration"].(map[string]any)
	if security["passphrase"] != "not-rendered-secret" || security["pmfMode"] != "OPTIONAL" || security["fastRoamingEnabled"] != false {
		t.Fatalf("updated security = %#v", security)
	}
}

func TestOfficialWlanOpenToEnterpriseDoesNotRequirePersonalPassphrase(t *testing.T) {
	doc := officialWlanDocument()
	doc["securityConfiguration"] = map[string]any{"type": "OPEN"}
	api := networklessWlanMutationAPI(doc)
	p, _, err := domain.NewWlanService(api).Update(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{
		Security: "wpa2-enterprise", SetSecurity: true,
		RadiusProfileID: radiusProfileID, SetRadiusProfileID: true,
		RadiusNASIDSource: "site-name", SetRadiusNASIDSource: true,
		COAEnabled: false, SetCOAEnabled: true,
	})
	if err != nil || p.Changes[0].ID == "" {
		t.Fatalf("open to enterprise plan = %#v, error = %v", p, err)
	}
}

func TestOfficialWlanPasswordUpdatePlanMasksChangedSecret(t *testing.T) {
	const secret = "new-password-not-for-plan"
	p, _, err := domain.NewWlanService(wlanMutationAPI()).Update(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{Password: secret, SetPassword: true})
	if err != nil {
		t.Fatal(err)
	}
	after := p.Changes[0].After.(map[string]any)
	if after["password"] != "***" {
		t.Fatalf("masked password marker = %#v", after["password"])
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), secret) {
		t.Fatalf("plan leaked changed password: %s", encoded)
	}
}
