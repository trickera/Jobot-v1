package server

import "testing"

func TestCanonicalResumeValidate(t *testing.T) {
	cases := []struct {
		name    string
		resume  CanonicalResume
		wantErr bool
	}{
		{
			name: "valid",
			resume: CanonicalResume{
				Basics:     ResumeBasics{Name: "Jane Doe"},
				Experience: []ResumeExperience{{Company: "Acme", Start: "2020-01", End: "2022-06"}},
			},
			wantErr: false,
		},
		{
			name:    "missing name",
			resume:  CanonicalResume{Basics: ResumeBasics{Name: "  "}},
			wantErr: true,
		},
		{
			name: "end before start",
			resume: CanonicalResume{
				Basics:     ResumeBasics{Name: "Jane Doe"},
				Experience: []ResumeExperience{{Company: "Acme", Start: "2022-01", End: "2020-01"}},
			},
			wantErr: true,
		},
		{
			name: "present end is always valid",
			resume: CanonicalResume{
				Basics:     ResumeBasics{Name: "Jane Doe"},
				Experience: []ResumeExperience{{Company: "Acme", Start: "2022-01", End: "present"}},
			},
			wantErr: false,
		},
		{
			name: "year-only dates compare correctly",
			resume: CanonicalResume{
				Basics:     ResumeBasics{Name: "Jane Doe"},
				Experience: []ResumeExperience{{Company: "Acme", Start: "2022", End: "2021"}},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.resume.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestNormalizeCanonicalTrimsAndDedupes(t *testing.T) {
	resume := CanonicalResume{
		Basics: ResumeBasics{
			Name:  "  Jane Doe  ",
			Links: []ResumeLink{{Label: " GitHub ", URL: " https://github.com/jane "}, {Label: "empty", URL: "  "}},
		},
		Summary: "  Backend engineer.  ",
		Skills: ResumeSkills{
			Hard: []string{" AWS ", "aws", "Terraform", "  "},
		},
		ConfirmedSkills: []string{" Kubernetes ", "kubernetes", "  "},
		Licenses: []ResumeLicense{
			{Name: " Registered Nurse ", Jurisdiction: " State of Illinois ", Number: "  "},
			{Name: "  "},
		},
		Experience: []ResumeExperience{
			{Company: " Acme ", Role: " Engineer ", Bullets: []string{" Built things. ", "  "}},
			{Company: "", Role: "", Bullets: nil},
		},
	}

	got := normalizeCanonical(resume)

	if got.Basics.Name != "Jane Doe" {
		t.Fatalf("expected trimmed name, got %q", got.Basics.Name)
	}
	if len(got.Basics.Links) != 1 || got.Basics.Links[0].URL != "https://github.com/jane" {
		t.Fatalf("expected empty-url link removed and remaining trimmed, got %+v", got.Basics.Links)
	}
	if got.Summary != "Backend engineer." {
		t.Fatalf("expected trimmed summary, got %q", got.Summary)
	}
	if len(got.Skills.Hard) != 2 {
		t.Fatalf("expected AWS/Terraform deduped (case-insensitive) to 2 entries, got %v", got.Skills.Hard)
	}
	if len(got.ConfirmedSkills) != 1 || got.ConfirmedSkills[0] != "Kubernetes" {
		t.Fatalf("expected confirmed skills trimmed/deduped to [Kubernetes], got %v", got.ConfirmedSkills)
	}
	if len(got.Licenses) != 1 || got.Licenses[0].Name != "Registered Nurse" || got.Licenses[0].Jurisdiction != "State of Illinois" {
		t.Fatalf("expected licenses trimmed and empty entries dropped, got %+v", got.Licenses)
	}
	if len(got.Experience) != 1 {
		t.Fatalf("expected empty experience entry dropped, got %+v", got.Experience)
	}
	if got.Experience[0].Company != "Acme" || len(got.Experience[0].Bullets) != 1 {
		t.Fatalf("expected trimmed experience with empty bullet dropped, got %+v", got.Experience[0])
	}
	if got.SchemaVersion != currentResumeSchemaVersion {
		t.Fatalf("expected schemaVersion defaulted to %d, got %d", currentResumeSchemaVersion, got.SchemaVersion)
	}
}

func TestCanonicalResumeJSONRoundTrip(t *testing.T) {
	resume := CanonicalResume{
		SchemaVersion: currentResumeSchemaVersion,
		Basics:        ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"},
		Target:        ResumeTarget{JobTitle: "DevOps Engineer", Category: "tech", Seniority: "Pleno"},
		Summary:       "Backend engineer.",
		Skills:        ResumeSkills{Hard: []string{"AWS"}, Soft: []string{"Communication"}, Tools: []string{"Docker"}},
		Experience: []ResumeExperience{
			{Company: "Acme", Role: "Engineer", Start: "2020-01", End: "present", Bullets: []string{"Built things."}},
		},
		Licenses: []ResumeLicense{{Name: "Registered Nurse", Jurisdiction: "State of Illinois"}},
	}
	if err := resume.Validate(); err != nil {
		t.Fatalf("expected valid resume, got %v", err)
	}
}

func TestNormalizeCanonicalMigratesLegacySchemaVersion(t *testing.T) {
	got := normalizeCanonical(CanonicalResume{SchemaVersion: 1, Basics: ResumeBasics{Name: "Jane Doe"}})
	if got.SchemaVersion != currentResumeSchemaVersion {
		t.Fatalf("expected legacy schema version to migrate to %d, got %d", currentResumeSchemaVersion, got.SchemaVersion)
	}
}
