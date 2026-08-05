package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// The 2026-07-13 usability run, reduced to tests. A nurse configured "Registered
// Nurse", hybrid/on-site, Chicago — and the app searched the United States
// remote-only, scored on the AWS/Terraform keywords left over from a backend
// profile, and rejected every nursing job it found as "off-target".

func nurseConfig() appConfig {
	return normalizeConfig(appConfig{
		Form: configForm{
			// The stale value the old code could not let go of. It is still here,
			// on purpose: nothing may silently delete it, and nothing may silently
			// obey it.
			Roles:            "Backend Developer",
			SearchProfiles:   "Registered Nurse, ICU Nurse | Pleno, Senior",
			WorkMode:         "hybrid_onsite",
			OnsiteLocation:   "Chicago, IL",
			RemoteCountry:    "United States",
			Keywords:         "AWS, Terraform, Kubernetes, CI/CD",
			KeywordsForRoles: "Backend Developer",
		},
	})
}

// TestOnsiteSearchNeverForcesTheRemoteOnlyFilter is UX-015. The <select> emits
// "hybrid_onsite"; nothing in Go understood it; both passes were skipped; the
// empty-pipelines fallback reinstated a remote search against RemoteCountry.
func TestOnsiteSearchNeverForcesTheRemoteOnlyFilter(t *testing.T) {
	cases := []struct {
		name        string
		workMode    string
		onsite      string
		wantRemote  []bool
		wantTargets []string
	}{
		{"the token the UI actually emits", "hybrid_onsite", "Chicago, IL", []bool{true, false}, []string{"United States", "Chicago, IL"}},
		{"onsite", "onsite", "Chicago, IL", []bool{false}, []string{"Chicago, IL"}},
		{"presencial (legacy pt token)", "presencial", "Berlin", []bool{false}, []string{"Berlin"}},
		{"hybrid", "hybrid", "London", []bool{true, false}, []string{"United States", "London"}},
		{"remote", "remote", "Chicago, IL", []bool{true}, []string{"United States"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := normalizeConfig(appConfig{Form: configForm{
				Roles:          "Registered Nurse",
				WorkMode:       tc.workMode,
				OnsiteLocation: tc.onsite,
				RemoteCountry:  "United States",
			}})

			pipelines := modalityPipelines(config)
			if len(pipelines) != len(tc.wantRemote) {
				t.Fatalf("got %d pipelines %v, want %d", len(pipelines), pipelines, len(tc.wantRemote))
			}
			for i, p := range pipelines {
				if p.remote != tc.wantRemote[i] {
					t.Errorf("pipeline %d: remote=%v, want %v", i, p.remote, tc.wantRemote[i])
				}
				if p.location != tc.wantTargets[i] {
					t.Errorf("pipeline %d: location=%q, want %q", i, p.location, tc.wantTargets[i])
				}
			}

			// And the URL that actually leaves the app.
			for i, p := range pipelines {
				got := buildLinkedInURL("nurse", p.location, p.remote, 0, 24)
				hasRemoteFilter := strings.Contains(got, "f_WT=2")
				if hasRemoteFilter != p.remote {
					t.Errorf("pipeline %d (%s): f_WT=2 present=%v but remote=%v — %s",
						i, p.location, hasRemoteFilter, p.remote, got)
				}
				if !p.remote && !strings.Contains(got, "f_WT=1%2C3") {
					t.Errorf("pipeline %d: an on-site pass must ask for on-site/hybrid jobs — %s", i, got)
				}
			}
		})
	}
}

// TestOnsiteSearchTargetsTheCityNotTheRemoteCountry — the reported symptom.
func TestOnsiteSearchTargetsTheCityNotTheRemoteCountry(t *testing.T) {
	pipelines := modalityPipelines(nurseConfig())
	var sawChicago bool
	for _, p := range pipelines {
		if !p.remote && p.location == "Chicago, IL" {
			sawChicago = true
		}
	}
	if !sawChicago {
		t.Fatalf("a Chicago on-site search never queried Chicago: %v", pipelines)
	}
}

// TestPrefilterUsesTheProfileThatFetchedTheJob is UX-016. Seventeen nursing jobs,
// a valid key, an enabled AI toggle — and every single one was skipped as
// off-target because a stale "Backend Developer" sat in Form.Roles.
func TestPrefilterUsesTheProfileThatFetchedTheJob(t *testing.T) {
	config := nurseConfig()
	profiles := parseSearchProfiles(config)
	if len(profiles) != 1 {
		t.Fatalf("expected the nursing profile, got %v", profiles)
	}

	jobs := tagJobsWithProfile([]jobPost{
		{Title: "Registered Nurse Stepdown", Description: "ICU experience preferred."},
		{Title: "RN - Intensive Care Unit", Description: "Registered nurse, CRRT."},
		{Title: "ICU Nurse", Description: "Charge nurse duties."},
	}, profiles[0])

	for _, job := range jobs {
		if !titleRelevant(config, job) {
			t.Errorf("%q was rejected as off-target by the very search that fetched it", job.Title)
		}
	}

	// The filter must still do its job on genuine noise.
	noise := tagJobsWithProfile([]jobPost{{Title: "Diesel Mechanic", Description: "Heavy vehicles."}}, profiles[0])
	if titleRelevant(config, noise[0]) {
		t.Error("the prefilter passed a job with no relationship to the searched roles")
	}
}

// TestProfilesOnlyConfigCanStartASearch — the gate that kept the stale role alive.
func TestProfilesOnlyConfigCanStartASearch(t *testing.T) {
	config := normalizeConfig(appConfig{Form: configForm{
		Roles:          "",
		SearchProfiles: "Registered Nurse | Pleno, Senior",
	}})
	if !searchRolesConfigured(config) {
		t.Fatal("a profiles-only config is a configured search; refusing it is what forced a stale Form.Roles to stay populated")
	}
	if searchRolesConfigured(normalizeConfig(appConfig{})) {
		t.Fatal("a config with no roles anywhere must not start a search")
	}
}

// TestProfilesOverrideTheSimpleRoleAndSaySo is UX-001. The override may stay —
// it may not stay silent.
func TestProfilesOverrideTheSimpleRoleAndSaySo(t *testing.T) {
	eff := effectiveSearchConfig(nurseConfig())

	if eff.RolesSource != "profiles" {
		t.Errorf("RolesSource = %q, want %q", eff.RolesSource, "profiles")
	}
	if len(eff.IgnoredRoles) != 1 || eff.IgnoredRoles[0] != "Backend Developer" {
		t.Errorf("IgnoredRoles = %v, want the overridden simple role to be reported", eff.IgnoredRoles)
	}
	if !strings.Contains(eff.summary(), "IGNORADO") {
		t.Errorf("the plan shown to the user does not mention the overridden role: %s", eff.summary())
	}

	// No conflict to report when they agree.
	same := normalizeConfig(appConfig{Form: configForm{
		Roles:          "Registered Nurse",
		SearchProfiles: "Registered Nurse | Pleno",
	}})
	if len(effectiveSearchConfig(same).IgnoredRoles) != 0 {
		t.Error("reported an override when the simple role and the profile agree — that is noise, not a warning")
	}
}

// TestKeywordsInheritedAcrossAChangeOfProfessionAreFlagged is UX-014. They are
// not deleted — a silent reset is as bad as a silent inheritance — they are
// flagged, and the user decides.
func TestKeywordsInheritedAcrossAChangeOfProfessionAreFlagged(t *testing.T) {
	eff := effectiveSearchConfig(nurseConfig())
	if !eff.StaleKeywords {
		t.Fatal("AWS/Terraform/Kubernetes/CI-CD, written for a Backend Developer, were carried into a Registered Nurse search without a word")
	}
	if len(eff.ScoringTerms) == 0 {
		t.Fatal("the keywords must still be there — flagging them is not deleting them")
	}
	if !strings.Contains(eff.summary(), "ATENCAO") {
		t.Errorf("the plan does not warn about the inherited keywords: %s", eff.summary())
	}

	// Keywords written for the role being searched are not stale.
	fresh := normalizeConfig(appConfig{Form: configForm{
		Roles:            "Registered Nurse",
		Keywords:         "ICU, CRRT, Epic",
		KeywordsForRoles: "Registered Nurse",
	}})
	if effectiveSearchConfig(fresh).StaleKeywords {
		t.Error("keywords written for this very role were flagged as inherited")
	}
}

// TestRoleSetsOverlapIsProfessionAgnostic — no special case for nursing, and
// none for the QA personas. It is a word test, not a domain test.
func TestRoleSetsOverlapIsProfessionAgnostic(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"Backend Developer"}, []string{"Registered Nurse"}, false},
		{[]string{"Backend Developer"}, []string{"Backend Engineer"}, true},
		{[]string{"Registered Nurse"}, []string{"ICU Nurse"}, true},
		{[]string{"Financial Analyst"}, []string{"Marketing Analyst"}, true},
		{[]string{"Financial Analyst"}, []string{"Graphic Designer"}, false},
		{[]string{"Sous Chef"}, []string{"Head Chef"}, true},
		{[]string{"Sr Data Engineer"}, []string{"Jr Data Engineer"}, true},
	}
	for _, tc := range cases {
		if got := roleSetsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("roleSetsOverlap(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestNormalizeConfigCanonicalizesWorkModeAndDerivesRemoteOnly — the bad token
// must not round-trip through the store forever (CORE-6).
func TestNormalizeConfigCanonicalizesWorkModeAndDerivesRemoteOnly(t *testing.T) {
	for raw, want := range map[string]string{
		"hybrid_onsite": workModeHybrid,
		"hibrido":       workModeHybrid,
		"presencial":    workModeOnsite,
		"on-site":       workModeOnsite,
		"remoto":        workModeRemote,
		"":              workModeRemote,
		"garbage":       workModeRemote,
	} {
		got := normalizeConfig(appConfig{Form: configForm{WorkMode: raw}})
		if got.Form.WorkMode != want {
			t.Errorf("normalizeConfig(WorkMode=%q) = %q, want %q", raw, got.Form.WorkMode, want)
		}
		if wantRemoteOnly := want == workModeRemote; got.Toggles["remoteOnly"] != wantRemoteOnly {
			t.Errorf("WorkMode=%q: remoteOnly=%v, want %v — the toggle must follow the mode, not contradict it",
				raw, got.Toggles["remoteOnly"], wantRemoteOnly)
		}
	}
}

// TestSearchPlanShowsTheEffectiveConfiguration — everything the user could not
// see before pressing Search.
func TestSearchPlanShowsTheEffectiveConfiguration(t *testing.T) {
	plan := buildSearchPlan(nurseConfig())

	if plan.WorkMode != workModeHybrid {
		t.Errorf("WorkMode = %q, want %q", plan.WorkMode, workModeHybrid)
	}
	if len(plan.Roles) == 0 || plan.Roles[0] != "Registered Nurse" {
		t.Errorf("Roles = %v, want the profile roles", plan.Roles)
	}
	if len(plan.IgnoredRoles) == 0 {
		t.Error("the plan hides that the simple target role is being ignored")
	}
	if !plan.StaleKeywords {
		t.Error("the plan hides the inherited keywords")
	}
	var sawOnsiteChicago bool
	for _, loc := range plan.Locations {
		if !loc.Remote && loc.Location == "Chicago, IL" {
			sawOnsiteChicago = true
		}
	}
	if !sawOnsiteChicago {
		t.Errorf("the plan does not show the on-site Chicago pass: %v", plan.Locations)
	}
}

// A fresh database has no target role, keywords, or resume yet. Those empty
// slices are still arrays in the HTTP contract: JSON null makes the renderer's
// array operations crash before the user can reach Settings.
func TestSearchPlanFreshConfigEmitsArraysInsteadOfNull(t *testing.T) {
	encoded, err := json.Marshal(buildSearchPlan(defaultConfig()))
	if err != nil {
		t.Fatalf("marshal fresh search plan: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode fresh search plan: %v", err)
	}
	for _, field := range []string{"roles", "levels", "scoringTerms", "locations", "sources"} {
		if value, exists := payload[field]; !exists || value == nil {
			t.Errorf("fresh search plan field %q = %v, want a JSON array", field, value)
		}
	}
}
