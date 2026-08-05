package server

import (
	"fmt"
	"strings"
	"time"
)

// currentResumeSchemaVersion is bumped whenever CanonicalResume's shape
// changes in a way that requires migrating previously saved documents.
const currentResumeSchemaVersion = 2

type CanonicalResume struct {
	SchemaVersion  int                   `json:"schemaVersion"`
	Basics         ResumeBasics          `json:"basics"`
	Target         ResumeTarget          `json:"target"`
	Summary        string                `json:"summary"`
	Skills         ResumeSkills          `json:"skills"`
	Experience     []ResumeExperience    `json:"experience"`
	Education      []ResumeEducation     `json:"education"`
	Projects       []ResumeProject       `json:"projects"`
	Licenses       []ResumeLicense       `json:"licenses"`
	Certifications []ResumeCertification `json:"certifications"`
	Languages      []ResumeLanguage      `json:"languages"`
	// ConfirmedSkills are capabilities the user explicitly confirmed having
	// during gap analysis ("confirm before adding"). They are kept separate
	// from Skills so the UI can always show that they came from the user's
	// confirmation, not from the originally-parsed resume — and so the
	// anti-invention gate can treat them as allowed in future analyses without
	// silently rewriting them into the extracted skill lists.
	ConfirmedSkills []string `json:"confirmedSkills,omitempty"`
}

type ResumeBasics struct {
	Name     string       `json:"name"`
	Headline string       `json:"headline"`
	Email    string       `json:"email"`
	Phone    string       `json:"phone"`
	Location string       `json:"location"`
	Links    []ResumeLink `json:"links"`
}

type ResumeLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type ResumeTarget struct {
	JobTitle  string `json:"jobTitle"`
	Category  string `json:"category"`
	Seniority string `json:"seniority"`
}

type ResumeSkills struct {
	Hard  []string `json:"hard"`
	Soft  []string `json:"soft"`
	Tools []string `json:"tools"`
}

type ResumeExperience struct {
	Company  string   `json:"company"`
	Role     string   `json:"role"`
	Start    string   `json:"start"` // "YYYY-MM" ou "YYYY"
	End      string   `json:"end"`   // "YYYY-MM", "YYYY" ou "present"
	Location string   `json:"location"`
	Bullets  []string `json:"bullets"`
}

type ResumeEducation struct {
	Institution string `json:"institution"`
	Degree      string `json:"degree"`
	Area        string `json:"area"`
	Start       string `json:"start"`
	End         string `json:"end"`
}

type ResumeProject struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Bullets     []string `json:"bullets"`
}

type ResumeCertification struct {
	Name   string `json:"name"`
	Issuer string `json:"issuer"`
	Year   string `json:"year"`
}

type ResumeLicense struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	Jurisdiction string `json:"jurisdiction"`
	Number       string `json:"number"`
	Expires      string `json:"expires"`
}

type ResumeLanguage struct {
	Language string `json:"language"`
	Fluency  string `json:"fluency"`
}

// Validate checks the minimum shape a CanonicalResume must have to be
// usable by the rest of the pipeline (diagnose/gap/tailor/export): a name,
// and internally-consistent experience date ranges.
func (r CanonicalResume) Validate() error {
	if strings.TrimSpace(r.Basics.Name) == "" {
		return fmt.Errorf("%w: basics.name is required", errMissingName)
	}
	for i, exp := range r.Experience {
		start, startOK := parseResumeDate(exp.Start)
		end, endOK := parseResumeDate(exp.End)
		if startOK && endOK && end.Before(start) {
			return fmt.Errorf("experience[%d]: end (%s) is before start (%s)", i, exp.End, exp.Start)
		}
	}
	return nil
}

// parseResumeDate parses "YYYY-MM" or "YYYY" into a comparable time.Time.
// "present" (case-insensitive) and empty/unparseable values report ok=false
// so callers can skip the comparison instead of treating them as an error.
func parseResumeDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "present") {
		return time.Time{}, false
	}
	if t, err := time.Parse("2006-01", value); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006", value); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// normalizeCanonical trims whitespace, drops empty list entries and
// deduplicates skills (case/accent-insensitive) without altering the
// meaning of the resume content.
func normalizeCanonical(r CanonicalResume) CanonicalResume {
	r.Basics.Name = strings.TrimSpace(r.Basics.Name)
	r.Basics.Headline = strings.TrimSpace(r.Basics.Headline)
	r.Basics.Email = strings.TrimSpace(r.Basics.Email)
	r.Basics.Phone = strings.TrimSpace(r.Basics.Phone)
	r.Basics.Location = strings.TrimSpace(r.Basics.Location)
	r.Basics.Links = trimLinks(r.Basics.Links)

	r.Target.JobTitle = strings.TrimSpace(r.Target.JobTitle)
	r.Target.Category = strings.TrimSpace(r.Target.Category)
	r.Target.Seniority = strings.TrimSpace(r.Target.Seniority)

	r.Summary = strings.TrimSpace(r.Summary)

	r.Skills.Hard = dedupeStrings(trimStrings(r.Skills.Hard))
	r.Skills.Soft = dedupeStrings(trimStrings(r.Skills.Soft))
	r.Skills.Tools = dedupeStrings(trimStrings(r.Skills.Tools))
	r.ConfirmedSkills = dedupeStrings(trimStrings(r.ConfirmedSkills))

	experience := make([]ResumeExperience, 0, len(r.Experience))
	for _, exp := range r.Experience {
		exp.Company = strings.TrimSpace(exp.Company)
		exp.Role = strings.TrimSpace(exp.Role)
		exp.Start = strings.TrimSpace(exp.Start)
		exp.End = strings.TrimSpace(exp.End)
		exp.Location = strings.TrimSpace(exp.Location)
		exp.Bullets = trimStrings(exp.Bullets)
		if exp.Company == "" && exp.Role == "" && len(exp.Bullets) == 0 {
			continue
		}
		experience = append(experience, exp)
	}
	r.Experience = experience

	education := make([]ResumeEducation, 0, len(r.Education))
	for _, edu := range r.Education {
		edu.Institution = strings.TrimSpace(edu.Institution)
		edu.Degree = strings.TrimSpace(edu.Degree)
		edu.Area = strings.TrimSpace(edu.Area)
		edu.Start = strings.TrimSpace(edu.Start)
		edu.End = strings.TrimSpace(edu.End)
		if edu.Institution == "" && edu.Degree == "" && edu.Area == "" {
			continue
		}
		education = append(education, edu)
	}
	r.Education = education

	projects := make([]ResumeProject, 0, len(r.Projects))
	for _, proj := range r.Projects {
		proj.Name = strings.TrimSpace(proj.Name)
		proj.Description = strings.TrimSpace(proj.Description)
		proj.URL = strings.TrimSpace(proj.URL)
		proj.Bullets = trimStrings(proj.Bullets)
		if proj.Name == "" && proj.Description == "" && len(proj.Bullets) == 0 {
			continue
		}
		projects = append(projects, proj)
	}
	r.Projects = projects

	licenses := make([]ResumeLicense, 0, len(r.Licenses))
	for _, license := range r.Licenses {
		license.Name = strings.TrimSpace(license.Name)
		license.Issuer = strings.TrimSpace(license.Issuer)
		license.Jurisdiction = strings.TrimSpace(license.Jurisdiction)
		license.Number = strings.TrimSpace(license.Number)
		license.Expires = strings.TrimSpace(license.Expires)
		if license.Name == "" {
			continue
		}
		licenses = append(licenses, license)
	}
	r.Licenses = licenses

	certifications := make([]ResumeCertification, 0, len(r.Certifications))
	for _, cert := range r.Certifications {
		cert.Name = strings.TrimSpace(cert.Name)
		cert.Issuer = strings.TrimSpace(cert.Issuer)
		cert.Year = strings.TrimSpace(cert.Year)
		if cert.Name == "" {
			continue
		}
		certifications = append(certifications, cert)
	}
	r.Certifications = certifications

	languages := make([]ResumeLanguage, 0, len(r.Languages))
	for _, lang := range r.Languages {
		lang.Language = strings.TrimSpace(lang.Language)
		lang.Fluency = strings.TrimSpace(lang.Fluency)
		if lang.Language == "" {
			continue
		}
		languages = append(languages, lang)
	}
	r.Languages = languages

	if r.SchemaVersion < currentResumeSchemaVersion {
		r.SchemaVersion = currentResumeSchemaVersion
	}
	return r
}

func trimLinks(links []ResumeLink) []ResumeLink {
	out := make([]ResumeLink, 0, len(links))
	for _, link := range links {
		link.Label = strings.TrimSpace(link.Label)
		link.URL = strings.TrimSpace(link.URL)
		if link.URL == "" {
			continue
		}
		out = append(out, link)
	}
	return out
}

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

// dedupeStrings removes case/accent-insensitive duplicates while keeping
// the first occurrence's original casing.
func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		key := normalizeText(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}
