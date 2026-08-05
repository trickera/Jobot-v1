package server

import "testing"

func TestParseSearchProfilesMultiLine(t *testing.T) {
	config := defaultConfig()
	config.Form.SearchProfiles = "Analista de infraestrutura, Infrastructure Analyst | Pleno, Senior | Lead, Staff | 8\nDevOps, SRE | Junior, Pleno | Senior, Lead | 5\nAnalista de suporte | Pleno, Senior |  | "

	profiles := parseSearchProfiles(config)
	if len(profiles) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(profiles))
	}

	if profiles[0].Name != "Analista de infraestrutura" {
		t.Fatalf("unexpected profile name %q", profiles[0].Name)
	}
	if len(profiles[0].Roles) != 2 {
		t.Fatalf("expected 2 roles in profile 0, got %v", profiles[0].Roles)
	}
	if profiles[1].MaxYears != 5 {
		t.Fatalf("expected DevOps profile maxYears=5, got %d", profiles[1].MaxYears)
	}
	// Third profile leaves excluded/maxYears blank -> falls back to global.
	if profiles[2].MaxYears != config.Form.MaxYears {
		t.Fatalf("expected support profile to inherit global maxYears, got %d", profiles[2].MaxYears)
	}
}

func TestParseSearchProfilesFallsBackToGlobal(t *testing.T) {
	config := defaultConfig()
	config.Form.SearchProfiles = ""
	config.Form.Roles = "DevOps Engineer, SRE"
	config.Form.Levels = "Pleno"

	profiles := parseSearchProfiles(config)
	if len(profiles) != 1 {
		t.Fatalf("expected single global profile, got %d", len(profiles))
	}
	if profiles[0].explicit {
		t.Fatal("global fallback profile should not be marked explicit")
	}
	if len(profiles[0].Roles) != 2 {
		t.Fatalf("expected 2 roles from global config, got %v", profiles[0].Roles)
	}
}

func TestProfileSeniorityIsIndependentPerProfile(t *testing.T) {
	config := defaultConfig()
	config.Form.SearchProfiles = "Analista de infraestrutura | Pleno, Senior | Lead, Staff | 8\nDevOps | Junior, Pleno | Senior, Lead | 5"
	profiles := parseSearchProfiles(config)

	infra := profiles[0]
	devops := profiles[1]

	infraSenior := jobPost{Title: "Analista de Infraestrutura Senior"}
	infraSenior = tagOne(infraSenior, infra)
	if reason := seniorityBlockReason(config, infraSenior); reason != "" {
		t.Fatalf("infra profile should allow Senior, got %q", reason)
	}

	devopsSenior := jobPost{Title: "DevOps Senior"}
	devopsSenior = tagOne(devopsSenior, devops)
	if reason := seniorityBlockReason(config, devopsSenior); reason == "" {
		t.Fatal("devops profile should block Senior")
	}

	devopsJunior := jobPost{Title: "DevOps Junior"}
	devopsJunior = tagOne(devopsJunior, devops)
	if reason := seniorityBlockReason(config, devopsJunior); reason != "" {
		t.Fatalf("devops profile should allow Junior, got %q", reason)
	}
}

func TestProfileQueryGroupsRolesOnly(t *testing.T) {
	profile := searchProfile{Roles: []string{"DevOps", "SRE", "Platform Engineer", "Cloud Engineer", "Infra", "Backend"}}
	groups := profile.queryGroups(5)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for 6 roles, got %d", len(groups))
	}
	for _, group := range groups {
		if group == "" {
			t.Fatal("group should not be empty")
		}
	}
}

func tagOne(job jobPost, profile searchProfile) jobPost {
	return tagJobsWithProfile([]jobPost{job}, profile)[0]
}
