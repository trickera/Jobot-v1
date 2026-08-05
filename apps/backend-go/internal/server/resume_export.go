package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontfamily"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/ledongthuc/pdf"
)

// pdfPageCount reports how many pages a generated PDF has, read straight from
// the bytes (no temp file). Used by the one-page auto-fit in exportPDF.
func pdfPageCount(pdfBytes []byte) (int, error) {
	reader, err := pdf.NewReader(bytes.NewReader(pdfBytes), int64(len(pdfBytes)))
	if err != nil {
		return 0, err
	}
	return reader.NumPage(), nil
}

// resumeTemplateStyle captures the small set of pure-Go PDF layout knobs
// that vary between templates. Every style keeps the resume single-column
// and text-selectable (Global Constraint) — only accent color, a thin rule
// under the name, and date alignment change.
type resumeTemplateStyle struct {
	accent            *props.Color
	ruleUnderName     bool // thin horizontal rule under name+contact (ATS Clean)
	ruleUnderHeading  bool // thin rule under each section heading (ATS Strict/Clean)
	coloredHeadings   bool // section headings use accent instead of black
	datesRightAligned bool // role/degree line uses a 2-col row, date right-aligned (Modern Accent)
	centerHeader      bool // name/headline/contact block centered (Modern Accent)
}

// resumeStyleForTemplate derives the PDF style from the template id. This is
// not a multi-column body layout: the "dates right-aligned" style still
// places the date on the same line as the role/degree (a single row split
// into two cells), so the linear top-to-bottom reading order used by ATS
// parsers is unaffected.
func resumeStyleForTemplate(tmpl resumeTemplate) resumeTemplateStyle {
	switch tmpl.ID {
	case resumeATSCleanTemplateID:
		return resumeTemplateStyle{
			accent:           &props.Color{Red: 30, Green: 90, Blue: 168},
			ruleUnderName:    true,
			ruleUnderHeading: true,
			coloredHeadings:  true,
		}
	case resumeModernAccentTemplateID:
		return resumeTemplateStyle{
			accent:            &props.Color{Red: 109, Green: 40, Blue: 217},
			coloredHeadings:   true,
			datesRightAligned: true,
			centerHeader:      true,
		}
	default: // ATS Strict — plain black, ruled headings, dates inline
		return resumeTemplateStyle{
			ruleUnderHeading: true,
		}
	}
}

// resumeHeadingRuleColor is the color of the thin rule drawn under section
// headings: the template accent when there is one, otherwise a neutral dark
// gray so the ATS Strict template still gets a clean separator (not a heavy
// black bar) without an accent.
func resumeHeadingRuleColor(style resumeTemplateStyle) *props.Color {
	if style.accent != nil {
		return style.accent
	}
	return &props.Color{Red: 90, Green: 90, Blue: 90}
}

// The skill taxonomy, the matcher and the anti-invention invariant that governs
// every label emitted below now live in resume_skill_taxonomy.go. All four
// exporters (Markdown, HTML, DOCX, PDF) and the preview call the single
// groupSkills there, so a category can never differ between what the user
// reviews and what the employer receives.

func resumeAccentHex(style resumeTemplateStyle) string {
	if style.accent == nil {
		return ""
	}
	return fmt.Sprintf("%02x%02x%02x", style.accent.Red, style.accent.Green, style.accent.Blue)
}

// resumeTemplateCSS derives HTML preview/export styling from the same knobs
// used by the pure-Go PDF renderer. It keeps the body single-column and only
// mirrors accent color, heading color, name rule, and date alignment.
func resumeTemplateCSS(tmpl resumeTemplate) string {
	style := resumeStyleForTemplate(tmpl)
	var b strings.Builder
	b.WriteString("body{font-family:Helvetica,Arial,sans-serif;font-size:10.5pt;color:#111;max-width:760px;margin:24px auto;padding:0 16px}")
	b.WriteString("h1{font-size:16pt;margin:0 0 2px}")
	b.WriteString("h2{font-size:12pt;text-transform:uppercase;margin:14px 0 4px}")
	b.WriteString(".role-line{margin:8px 0 2px;font-size:11pt;overflow:auto}")
	b.WriteString(".dates{font-size:9.5pt;color:#333}")
	b.WriteString("ul{margin:2px 0 6px 18px;padding:0}")
	if hex := resumeAccentHex(style); hex != "" {
		accent := "#" + hex
		if style.coloredHeadings {
			b.WriteString("h1{color:" + accent + "}h2{color:" + accent + "}")
		}
		if style.ruleUnderName {
			b.WriteString(".contact-line{border-bottom:1px solid " + accent + ";padding-bottom:6px}")
		}
	}
	if style.datesRightAligned {
		b.WriteString(".dates{float:right}")
	} else {
		b.WriteString(".dates{display:block}")
	}
	return b.String()
}

// resumeContactLine joins the header contact details (email, phone,
// location, links) into a single text line — kept in-body (never a
// header/footer), per the ATS-safe layout rules (G.3).
func resumeContactLine(r CanonicalResume) string {
	var parts []string
	if v := strings.TrimSpace(r.Basics.Email); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(r.Basics.Phone); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(r.Basics.Location); v != "" {
		parts = append(parts, v)
	}
	for _, link := range r.Basics.Links {
		if v := strings.TrimSpace(link.URL); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " | ")
}

func experienceDateRange(e ResumeExperience) string {
	return fmt.Sprintf("%s – %s", coalesce(e.Start, "?"), coalesce(e.End, "present"))
}

func educationDateRange(e ResumeEducation) string {
	return fmt.Sprintf("%s – %s", coalesce(e.Start, "?"), coalesce(e.End, "?"))
}

func resumeLicenseLine(license ResumeLicense) string {
	line := license.Name
	if license.Jurisdiction != "" {
		line += " — " + license.Jurisdiction
	} else if license.Issuer != "" {
		line += " — " + license.Issuer
	}
	if license.Number != "" {
		line += " · " + license.Number
	}
	if license.Expires != "" {
		line += " (expires " + license.Expires + ")"
	}
	return line
}

// exportMarkdown renders the ATS Strict layout as Markdown (G.3): "#" name,
// "##" sections in the fixed order (skipping empty ones), "-" bullets.
func exportMarkdown(r CanonicalResume) string {
	var b strings.Builder

	b.WriteString("# " + coalesce(r.Basics.Name, "Resume") + "\n\n")
	if contact := resumeContactLine(r); contact != "" {
		b.WriteString(contact + "\n\n")
	}

	if strings.TrimSpace(r.Summary) != "" {
		b.WriteString("## SUMMARY\n\n" + r.Summary + "\n\n")
	}

	if len(r.Experience) > 0 {
		b.WriteString("## EXPERIENCE\n\n")
		for _, exp := range r.Experience {
			b.WriteString(fmt.Sprintf("**%s — %s** (%s)\n\n", exp.Role, exp.Company, experienceDateRange(exp)))
			for _, bullet := range exp.Bullets {
				b.WriteString("- " + bullet + "\n")
			}
			b.WriteString("\n")
		}
	}

	if len(r.Education) > 0 {
		b.WriteString("## EDUCATION\n\n")
		for _, edu := range r.Education {
			b.WriteString(fmt.Sprintf("**%s — %s** (%s)\n\n", edu.Degree, edu.Institution, educationDateRange(edu)))
		}
	}

	if total := len(r.Skills.Hard) + len(r.Skills.Soft) + len(r.Skills.Tools); total > 0 {
		b.WriteString("## SKILLS\n\n")
		if groups, grouped := groupSkills(r.Skills); grouped {
			for _, g := range groups {
				b.WriteString("- **" + g.label + ":** " + strings.Join(g.items, ", ") + "\n")
			}
		} else {
			if len(r.Skills.Hard) > 0 {
				b.WriteString("- Hard skills: " + strings.Join(r.Skills.Hard, ", ") + "\n")
			}
			if len(r.Skills.Soft) > 0 {
				b.WriteString("- Soft skills: " + strings.Join(r.Skills.Soft, ", ") + "\n")
			}
			if len(r.Skills.Tools) > 0 {
				b.WriteString("- Tools: " + strings.Join(r.Skills.Tools, ", ") + "\n")
			}
		}
		b.WriteString("\n")
	}

	if len(r.Projects) > 0 {
		b.WriteString("## PROJECTS\n\n")
		for _, p := range r.Projects {
			b.WriteString("**" + p.Name + "**\n\n")
			if strings.TrimSpace(p.Description) != "" {
				b.WriteString(p.Description + "\n\n")
			}
			for _, bullet := range p.Bullets {
				b.WriteString("- " + bullet + "\n")
			}
			b.WriteString("\n")
		}
	}

	if len(r.Licenses) > 0 {
		b.WriteString("## LICENSES\n\n")
		for _, license := range r.Licenses {
			b.WriteString("- " + resumeLicenseLine(license) + "\n")
		}
		b.WriteString("\n")
	}

	if len(r.Certifications) > 0 {
		b.WriteString("## CERTIFICATIONS\n\n")
		for _, c := range r.Certifications {
			line := "- " + c.Name
			if c.Issuer != "" {
				line += " — " + c.Issuer
			}
			if c.Year != "" {
				line += " (" + c.Year + ")"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(r.Languages) > 0 {
		b.WriteString("## LANGUAGES\n\n")
		for _, l := range r.Languages {
			line := "- " + l.Language
			if l.Fluency != "" {
				line += " — " + l.Fluency
			}
			b.WriteString(line + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// exportHTML renders the selected single-column template as semantic HTML,
// with all user values escaped and styling derived from resumeTemplateCSS.
func exportHTML(r CanonicalResume, tmpl resumeTemplate) string {
	var b strings.Builder
	name := coalesce(r.Basics.Name, "Resume")

	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<title>" + html.EscapeString(name) + "</title>\n")
	b.WriteString("<style>" + resumeTemplateCSS(tmpl) + "</style>\n</head>\n<body>\n")
	b.WriteString("<h1>" + html.EscapeString(name) + "</h1>\n")
	if contact := resumeContactLine(r); contact != "" {
		b.WriteString("<p class=\"contact-line\">" + html.EscapeString(contact) + "</p>\n")
	}

	if strings.TrimSpace(r.Summary) != "" {
		b.WriteString("<h2>Summary</h2>\n<p>" + html.EscapeString(r.Summary) + "</p>\n")
	}

	if len(r.Experience) > 0 {
		b.WriteString("<h2>Experience</h2>\n")
		for _, exp := range r.Experience {
			b.WriteString("<p class=\"role-line\"><strong>" + html.EscapeString(exp.Role) + " — " + html.EscapeString(exp.Company) + "</strong> <span class=\"dates\">" + html.EscapeString(experienceDateRange(exp)) + "</span></p>\n")
			if len(exp.Bullets) > 0 {
				b.WriteString("<ul>\n")
				for _, bullet := range exp.Bullets {
					b.WriteString("<li>" + html.EscapeString(bullet) + "</li>\n")
				}
				b.WriteString("</ul>\n")
			}
		}
	}

	if len(r.Education) > 0 {
		b.WriteString("<h2>Education</h2>\n")
		for _, edu := range r.Education {
			b.WriteString("<p class=\"role-line\"><strong>" + html.EscapeString(edu.Degree) + " — " + html.EscapeString(edu.Institution) + "</strong> <span class=\"dates\">" + html.EscapeString(educationDateRange(edu)) + "</span></p>\n")
		}
	}

	if total := len(r.Skills.Hard) + len(r.Skills.Soft) + len(r.Skills.Tools); total > 0 {
		b.WriteString("<h2>Skills</h2>\n<ul>\n")
		if groups, grouped := groupSkills(r.Skills); grouped {
			for _, g := range groups {
				b.WriteString("<li><strong>" + html.EscapeString(g.label) + ":</strong> " + html.EscapeString(strings.Join(g.items, ", ")) + "</li>\n")
			}
		} else {
			if len(r.Skills.Hard) > 0 {
				b.WriteString("<li>Hard skills: " + html.EscapeString(strings.Join(r.Skills.Hard, ", ")) + "</li>\n")
			}
			if len(r.Skills.Soft) > 0 {
				b.WriteString("<li>Soft skills: " + html.EscapeString(strings.Join(r.Skills.Soft, ", ")) + "</li>\n")
			}
			if len(r.Skills.Tools) > 0 {
				b.WriteString("<li>Tools: " + html.EscapeString(strings.Join(r.Skills.Tools, ", ")) + "</li>\n")
			}
		}
		b.WriteString("</ul>\n")
	}

	if len(r.Projects) > 0 {
		b.WriteString("<h2>Projects</h2>\n")
		for _, p := range r.Projects {
			b.WriteString("<h3>" + html.EscapeString(p.Name) + "</h3>\n")
			if strings.TrimSpace(p.Description) != "" {
				b.WriteString("<p>" + html.EscapeString(p.Description) + "</p>\n")
			}
			if len(p.Bullets) > 0 {
				b.WriteString("<ul>\n")
				for _, bullet := range p.Bullets {
					b.WriteString("<li>" + html.EscapeString(bullet) + "</li>\n")
				}
				b.WriteString("</ul>\n")
			}
		}
	}

	if len(r.Licenses) > 0 {
		b.WriteString("<h2>Licenses</h2>\n<ul>\n")
		for _, license := range r.Licenses {
			b.WriteString("<li>" + html.EscapeString(resumeLicenseLine(license)) + "</li>\n")
		}
		b.WriteString("</ul>\n")
	}

	if len(r.Certifications) > 0 {
		b.WriteString("<h2>Certifications</h2>\n<ul>\n")
		for _, c := range r.Certifications {
			line := c.Name
			if c.Issuer != "" {
				line += " — " + c.Issuer
			}
			if c.Year != "" {
				line += " (" + c.Year + ")"
			}
			b.WriteString("<li>" + html.EscapeString(line) + "</li>\n")
		}
		b.WriteString("</ul>\n")
	}

	if len(r.Languages) > 0 {
		b.WriteString("<h2>Languages</h2>\n<ul>\n")
		for _, l := range r.Languages {
			line := l.Language
			if l.Fluency != "" {
				line += " — " + l.Fluency
			}
			b.WriteString("<li>" + html.EscapeString(line) + "</li>\n")
		}
		b.WriteString("</ul>\n")
	}

	b.WriteString("</body>\n</html>\n")
	return b.String()
}

// exportPDF renders the ATS Strict layout pure-Go (maroto/v2 → gofpdf):
// single column, Helvetica, UPPERCASE section headings, dates as plain
// text next to the role (never in a table). PageSize is "letter"
// (default) or "a4" (G.3). Produces a Unicode text layer that is
// selectable/extractable — validated against extractPDFText in tests.
// resumeDensity holds the margin + vertical-rhythm knobs the one-page
// auto-fit varies. Font sizes and side margins stay constant across levels so
// line length and readability never change — only the air between blocks.
type resumeDensity struct {
	topMargin    float64
	bottomMargin float64
	sectionTop   float64 // space above a section heading
	entryTop     float64 // space above an experience/education entry
	bodyTop      float64 // space above a bullet / body line
}

// resumeDensityLevels run from most comfortable to most compact. exportPDF
// keeps the most comfortable level that still fits a single page; if none
// does, the résumé is genuinely long and the compact level is used (2+ pages,
// but dense — never a lone orphan line on an otherwise empty page).
var resumeDensityLevels = []resumeDensity{
	{topMargin: 15, bottomMargin: 15, sectionTop: 7, entryTop: 4.5, bodyTop: 1.8},
	{topMargin: 13, bottomMargin: 13, sectionTop: 5.5, entryTop: 3.2, bodyTop: 1.3},
	{topMargin: 11, bottomMargin: 11, sectionTop: 4.2, entryTop: 2.2, bodyTop: 0.9},
}

const (
	resumeSideMargin       = 18.0 // generous left/right margin (mm) — no edge-bleeding text
	resumeGridSize         = 36   // fine grid so a bullet marker column is ~5mm (tight, professional)
	resumeBulletMarkerCols = 1    // narrow column holding just the "•"
	resumeBulletTextCols   = resumeGridSize - resumeBulletMarkerCols
	resumeEntryTitleCols   = 24 // 2/3 of the grid for a role/degree title
	resumeEntryDateCols    = resumeGridSize - resumeEntryTitleCols
	resumeNameSize         = 18.0
	resumeHeadingSize      = 11.0
	resumeBodySize         = 10.5
)

// exportPDF renders the résumé to a single-column, text-selectable PDF,
// trying progressively denser vertical rhythms until it fits one page (or the
// content is genuinely long enough to need more). The chosen page size and
// template style are constant across attempts.
func exportPDF(r CanonicalResume, tmpl resumeTemplate, pageSize string) ([]byte, error) {
	style := resumeStyleForTemplate(tmpl)
	size := pagesize.Letter
	if strings.EqualFold(strings.TrimSpace(pageSize), "a4") {
		size = pagesize.A4
	}

	var lastBytes []byte
	for i, d := range resumeDensityLevels {
		b, err := renderResumePDF(r, style, size, d)
		if err != nil {
			return nil, err
		}
		lastBytes = b
		if i == len(resumeDensityLevels)-1 {
			break
		}
		pages, err := pdfPageCount(b)
		if err != nil || pages <= 1 {
			// Fits one page (or we can't measure) — keep this comfortable level.
			return b, nil
		}
	}
	return lastBytes, nil
}

func renderResumePDF(r CanonicalResume, style resumeTemplateStyle, size pagesize.Type, d resumeDensity) ([]byte, error) {
	cfg := config.NewBuilder().
		WithPageSize(size).
		WithLeftMargin(resumeSideMargin).
		WithRightMargin(resumeSideMargin).
		WithTopMargin(d.topMargin).
		WithBottomMargin(d.bottomMargin).
		WithMaxGridSize(resumeGridSize).
		WithDefaultFont(&props.Font{Family: fontfamily.Helvetica, Size: resumeBodySize}).
		Build()

	m := maroto.New(cfg)

	headerAlign := align.Left
	if style.centerHeader {
		headerAlign = align.Center
	}

	nameProp := props.Text{Size: resumeNameSize, Style: fontstyle.Bold, Align: headerAlign}
	if style.coloredHeadings {
		nameProp.Color = style.accent
	}
	m.AddRows(text.NewAutoRow(coalesce(r.Basics.Name, "Resume"), nameProp))
	if headline := strings.TrimSpace(r.Basics.Headline); headline != "" {
		m.AddRows(text.NewAutoRow(headline, props.Text{Size: resumeBodySize, Style: fontstyle.Italic, Align: headerAlign, Top: 1.5}))
	}
	if contact := resumeContactLine(r); contact != "" {
		m.AddRows(text.NewAutoRow(contact, props.Text{Size: 9.5, Align: headerAlign, Top: 1.5}))
	}
	if style.ruleUnderName {
		m.AddRow(4.5, line.NewCol(resumeGridSize, props.Line{Color: style.accent, Thickness: 0.6, OffsetPercent: 45, SizePercent: 100}))
	}

	addSectionHeading := func(title string) {
		headingProp := props.Text{Size: resumeHeadingSize, Style: fontstyle.Bold, Top: d.sectionTop}
		if style.coloredHeadings {
			headingProp.Color = style.accent
		}
		m.AddRows(text.NewAutoRow(strings.ToUpper(title), headingProp))
		if style.ruleUnderHeading {
			m.AddRow(2.6, line.NewCol(resumeGridSize, props.Line{Color: resumeHeadingRuleColor(style), Thickness: 0.4, OffsetPercent: 55, SizePercent: 100}))
		}
	}

	// bullet renders a hanging-indented bullet: the "•" sits in a narrow left
	// column and the text in the wide column beside it, so wrapped lines align
	// under the text rather than back at the left margin.
	bullet := func(content string) {
		m.AddAutoRow(
			text.NewCol(resumeBulletMarkerCols, "•", props.Text{Size: resumeBodySize, Top: d.bodyTop}),
			text.NewCol(resumeBulletTextCols, content, props.Text{Size: resumeBodySize, Top: d.bodyTop}),
		)
	}

	// entryHeader renders a "Title — Org" line with the date range. datesRight
	// puts the date on the same row (right-aligned), matching a classic resume;
	// otherwise the date + optional meta go on an italic line below. detail is
	// the location/area shown as secondary text.
	entryHeader := func(title, dates, detail string) {
		title = strings.TrimSpace(title)
		detail = strings.TrimSpace(detail)
		if style.datesRightAligned {
			m.AddAutoRow(
				text.NewCol(resumeEntryTitleCols, title, props.Text{Size: 11, Style: fontstyle.Bold, Top: d.entryTop}),
				text.NewCol(resumeEntryDateCols, dates, props.Text{Size: 9.5, Style: fontstyle.Bold, Align: align.Right, Top: d.entryTop}),
			)
			if detail != "" {
				m.AddRows(text.NewAutoRow(detail, props.Text{Size: 9.5, Style: fontstyle.Italic}))
			}
			return
		}
		m.AddRows(text.NewAutoRow(title, props.Text{Size: 11, Style: fontstyle.Bold, Top: d.entryTop}))
		meta := dates
		if detail != "" {
			meta = dates + "  ·  " + detail
		}
		m.AddRows(text.NewAutoRow(meta, props.Text{Size: 9.5, Style: fontstyle.Italic}))
	}

	entryTitle := func(primary, org string) string {
		primary = strings.TrimSpace(primary)
		org = strings.TrimSpace(org)
		switch {
		case primary != "" && org != "":
			return primary + " — " + org
		case org != "":
			return org
		default:
			return primary
		}
	}

	addExperienceHeader := func(exp ResumeExperience) {
		entryHeader(entryTitle(exp.Role, exp.Company), experienceDateRange(exp), exp.Location)
	}

	addEducationHeader := func(edu ResumeEducation) {
		entryHeader(entryTitle(edu.Degree, edu.Institution), educationDateRange(edu), edu.Area)
	}

	if strings.TrimSpace(r.Summary) != "" {
		addSectionHeading("Summary")
		m.AddRows(text.NewAutoRow(r.Summary, props.Text{Size: resumeBodySize, Top: d.bodyTop}))
	}

	if len(r.Experience) > 0 {
		addSectionHeading("Experience")
		for _, exp := range r.Experience {
			addExperienceHeader(exp)
			for _, b := range exp.Bullets {
				bullet(b)
			}
		}
	}

	if len(r.Education) > 0 {
		addSectionHeading("Education")
		for _, edu := range r.Education {
			addEducationHeader(edu)
		}
	}

	if total := len(r.Skills.Hard) + len(r.Skills.Soft) + len(r.Skills.Tools); total > 0 {
		addSectionHeading("Skills")
		if groups, grouped := groupSkills(r.Skills); grouped {
			for _, g := range groups {
				m.AddRows(text.NewAutoRow(g.label+": "+strings.Join(g.items, ", "), props.Text{Size: resumeBodySize, Top: d.bodyTop}))
			}
		} else {
			if len(r.Skills.Hard) > 0 {
				m.AddRows(text.NewAutoRow("Hard skills: "+strings.Join(r.Skills.Hard, ", "), props.Text{Size: resumeBodySize, Top: d.bodyTop}))
			}
			if len(r.Skills.Soft) > 0 {
				m.AddRows(text.NewAutoRow("Soft skills: "+strings.Join(r.Skills.Soft, ", "), props.Text{Size: resumeBodySize, Top: d.bodyTop}))
			}
			if len(r.Skills.Tools) > 0 {
				m.AddRows(text.NewAutoRow("Tools: "+strings.Join(r.Skills.Tools, ", "), props.Text{Size: resumeBodySize, Top: d.bodyTop}))
			}
		}
	}

	if len(r.Projects) > 0 {
		addSectionHeading("Projects")
		for _, p := range r.Projects {
			m.AddRows(text.NewAutoRow(p.Name, props.Text{Size: 11, Style: fontstyle.Bold, Top: d.entryTop}))
			if strings.TrimSpace(p.Description) != "" {
				m.AddRows(text.NewAutoRow(p.Description, props.Text{Size: resumeBodySize, Top: d.bodyTop}))
			}
			for _, b := range p.Bullets {
				bullet(b)
			}
		}
	}

	if len(r.Licenses) > 0 {
		addSectionHeading("Licenses")
		for _, license := range r.Licenses {
			m.AddRows(text.NewAutoRow(resumeLicenseLine(license), props.Text{Size: resumeBodySize, Top: d.bodyTop}))
		}
	}

	if len(r.Certifications) > 0 {
		addSectionHeading("Certifications")
		for _, c := range r.Certifications {
			row := c.Name
			if c.Issuer != "" {
				row += " — " + c.Issuer
			}
			if c.Year != "" {
				row += " (" + c.Year + ")"
			}
			m.AddRows(text.NewAutoRow(row, props.Text{Size: resumeBodySize, Top: d.bodyTop}))
		}
	}

	if len(r.Languages) > 0 {
		addSectionHeading("Languages")
		for _, l := range r.Languages {
			row := l.Language
			if l.Fluency != "" {
				row += " — " + l.Fluency
			}
			m.AddRows(text.NewAutoRow(row, props.Text{Size: resumeBodySize, Top: d.bodyTop}))
		}
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("falha ao gerar PDF: %w", err)
	}
	return doc.GetBytes(), nil
}

func exportFileName(r CanonicalResume, ext string) string {
	name := strings.TrimSpace(r.Basics.Name)
	if name == "" {
		name = "resume"
	}
	slug := strings.Map(func(rn rune) rune {
		switch {
		case rn >= 'a' && rn <= 'z', rn >= '0' && rn <= '9':
			return rn
		case rn == ' ' || rn == '-' || rn == '_':
			return '-'
		default:
			return -1
		}
	}, normalizeText(name))
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "resume"
	}
	return slug + "." + ext
}

// resumeTemplatesList returns the built-in template catalog (id, name,
// category, engine, isAts), seeding it first if it hasn't been seeded yet
// (idempotent) — lets the frontend render a real, up-to-date list instead
// of hardcoding the ATS Strict default.
func (a *api) resumeTemplatesList(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := a.configStore.seedResumeTemplates(); err != nil {
		a.logger.Printf("seed resume templates: %v", err)
	}
	templates, err := a.configStore.listResumeTemplates()
	if err != nil {
		a.logger.Printf("list resume templates: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}
	if templates == nil {
		templates = []resumeTemplate{}
	}
	writeJSON(w, http.StatusOK, resumeTemplatesResponse{Templates: templates})
}

func (a *api) resumeExport(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload exportRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}
	canonical := normalizeCanonical(payload.Canonical)

	templateID := strings.TrimSpace(payload.TemplateID)
	if templateID == "" {
		templateID = resumeATSStrictTemplateID
	}
	tmpl := resumeTemplate{ID: resumeATSStrictTemplateID, Name: "ATS Strict", Category: "ats", Engine: "native", IsATS: true}
	if a.configStore != nil {
		if templates, err := a.configStore.listResumeTemplates(); err == nil {
			for _, t := range templates {
				if t.ID == templateID {
					tmpl = t
					break
				}
			}
		}
	}

	pageSize := strings.ToLower(strings.TrimSpace(payload.PageSize))
	if pageSize != "a4" {
		pageSize = "letter"
	}

	switch strings.ToLower(strings.TrimSpace(payload.Format)) {
	case "md", "markdown":
		writeJSON(w, http.StatusOK, exportResponse{
			Format:   "md",
			Content:  exportMarkdown(canonical),
			FileName: exportFileName(canonical, "md"),
		})
	case "html":
		writeJSON(w, http.StatusOK, exportResponse{
			Format:   "html",
			Content:  exportHTML(canonical, tmpl),
			FileName: exportFileName(canonical, "html"),
		})
	case "pdf":
		pdfBytes, err := exportPDF(canonical, tmpl, pageSize)
		if err != nil {
			a.logger.Printf("export resume pdf: %v", err)
			writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
			return
		}
		writeJSON(w, http.StatusOK, exportResponse{
			Format:   "pdf",
			Content:  base64.StdEncoding.EncodeToString(pdfBytes),
			FileName: exportFileName(canonical, "pdf"),
		})
	case "docx":
		docxBytes, err := exportDOCX(canonical, tmpl)
		if err != nil {
			a.logger.Printf("export resume docx: %v", err)
			writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
			return
		}
		writeJSON(w, http.StatusOK, exportResponse{
			Format:   "docx",
			Content:  base64.StdEncoding.EncodeToString(docxBytes),
			FileName: exportFileName(canonical, "docx"),
		})
	default:
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid export format. Use md, html, pdf or docx."))
	}
}
