package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

type docxParaOpts struct {
	bold       bool
	sizeHalfPt int
	colorHex   string
}

const docxHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`

const docxFooter = `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="720" w:right="720" w:bottom="720" w:left="720" w:header="0" w:footer="0" w:gutter="0"/></w:sectPr></w:body></w:document>`

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

const docxRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:rPr><w:rFonts w:ascii="Helvetica" w:hAnsi="Helvetica"/><w:sz w:val="21"/></w:rPr></w:style></w:styles>`

// exportDOCX renders an ATS-safe, single-column OOXML package using only the
// standard library. It mirrors the same section order and template knobs used
// by the PDF/HTML exporters.
func exportDOCX(r CanonicalResume, tmpl resumeTemplate) ([]byte, error) {
	style := resumeStyleForTemplate(tmpl)
	accent := resumeAccentHex(style)
	headingColor := docxHeadingColor(accent, style)

	var doc strings.Builder
	doc.WriteString(docxHeader)
	writeDOCXPara(&doc, coalesce(r.Basics.Name, "Resume"), docxParaOpts{bold: true, sizeHalfPt: 32, colorHex: headingColor})
	if contact := resumeContactLine(r); contact != "" {
		writeDOCXPara(&doc, contact, docxParaOpts{sizeHalfPt: 20})
	}

	if strings.TrimSpace(r.Summary) != "" {
		writeDOCXHeading(&doc, "SUMMARY", headingColor)
		writeDOCXPara(&doc, r.Summary, docxParaOpts{sizeHalfPt: 21})
	}

	if len(r.Experience) > 0 {
		writeDOCXHeading(&doc, "EXPERIENCE", headingColor)
		for _, exp := range r.Experience {
			writeDOCXTitleDate(&doc, exp.Role+" — "+exp.Company, experienceDateRange(exp), style)
			for _, bullet := range exp.Bullets {
				writeDOCXPara(&doc, "- "+bullet, docxParaOpts{sizeHalfPt: 21})
			}
		}
	}

	if len(r.Education) > 0 {
		writeDOCXHeading(&doc, "EDUCATION", headingColor)
		for _, edu := range r.Education {
			title := edu.Degree + " — " + edu.Institution
			if strings.TrimSpace(edu.Area) != "" {
				title += " — " + edu.Area
			}
			writeDOCXTitleDate(&doc, title, educationDateRange(edu), style)
		}
	}

	if len(r.Skills.Hard)+len(r.Skills.Soft)+len(r.Skills.Tools) > 0 {
		writeDOCXHeading(&doc, "SKILLS", headingColor)
		if groups, grouped := groupSkills(r.Skills); grouped {
			for _, g := range groups {
				writeDOCXPara(&doc, g.label+": "+strings.Join(g.items, ", "), docxParaOpts{sizeHalfPt: 21})
			}
		} else {
			if len(r.Skills.Hard) > 0 {
				writeDOCXPara(&doc, "Hard skills: "+strings.Join(r.Skills.Hard, ", "), docxParaOpts{sizeHalfPt: 21})
			}
			if len(r.Skills.Soft) > 0 {
				writeDOCXPara(&doc, "Soft skills: "+strings.Join(r.Skills.Soft, ", "), docxParaOpts{sizeHalfPt: 21})
			}
			if len(r.Skills.Tools) > 0 {
				writeDOCXPara(&doc, "Tools: "+strings.Join(r.Skills.Tools, ", "), docxParaOpts{sizeHalfPt: 21})
			}
		}
	}

	if len(r.Projects) > 0 {
		writeDOCXHeading(&doc, "PROJECTS", headingColor)
		for _, project := range r.Projects {
			writeDOCXPara(&doc, project.Name, docxParaOpts{bold: true, sizeHalfPt: 22})
			if strings.TrimSpace(project.Description) != "" {
				writeDOCXPara(&doc, project.Description, docxParaOpts{sizeHalfPt: 21})
			}
			if strings.TrimSpace(project.URL) != "" {
				writeDOCXPara(&doc, project.URL, docxParaOpts{sizeHalfPt: 20})
			}
			for _, bullet := range project.Bullets {
				writeDOCXPara(&doc, "- "+bullet, docxParaOpts{sizeHalfPt: 21})
			}
		}
	}

	if len(r.Licenses) > 0 {
		writeDOCXHeading(&doc, "LICENSES", headingColor)
		for _, license := range r.Licenses {
			writeDOCXPara(&doc, "- "+resumeLicenseLine(license), docxParaOpts{sizeHalfPt: 21})
		}
	}

	if len(r.Certifications) > 0 {
		writeDOCXHeading(&doc, "CERTIFICATIONS", headingColor)
		for _, cert := range r.Certifications {
			line := cert.Name
			if strings.TrimSpace(cert.Issuer) != "" {
				line += " — " + cert.Issuer
			}
			if strings.TrimSpace(cert.Year) != "" {
				line += " (" + cert.Year + ")"
			}
			writeDOCXPara(&doc, "- "+line, docxParaOpts{sizeHalfPt: 21})
		}
	}

	if len(r.Languages) > 0 {
		writeDOCXHeading(&doc, "LANGUAGES", headingColor)
		for _, language := range r.Languages {
			line := language.Language
			if strings.TrimSpace(language.Fluency) != "" {
				line += " — " + language.Fluency
			}
			writeDOCXPara(&doc, "- "+line, docxParaOpts{sizeHalfPt: 21})
		}
	}

	doc.WriteString(docxFooter)
	return docxPackage(doc.String())
}

func writeDOCXHeading(doc *strings.Builder, text string, colorHex string) {
	writeDOCXPara(doc, strings.ToUpper(text), docxParaOpts{bold: true, sizeHalfPt: 24, colorHex: colorHex})
}

func writeDOCXTitleDate(doc *strings.Builder, title string, dates string, style resumeTemplateStyle) {
	title = strings.TrimSpace(title)
	dates = strings.TrimSpace(dates)
	if style.datesRightAligned && dates != "" {
		doc.WriteString(`<w:p><w:pPr><w:tabs><w:tab w:val="right" w:pos="9000"/></w:tabs></w:pPr>`)
		writeDOCXRun(doc, title, docxParaOpts{bold: true, sizeHalfPt: 22})
		doc.WriteString(`<w:r><w:tab/></w:r>`)
		writeDOCXRun(doc, dates, docxParaOpts{sizeHalfPt: 19})
		doc.WriteString(`</w:p>`)
		return
	}
	writeDOCXPara(doc, title, docxParaOpts{bold: true, sizeHalfPt: 22})
	if dates != "" {
		writeDOCXPara(doc, dates, docxParaOpts{sizeHalfPt: 19})
	}
}

func writeDOCXPara(doc *strings.Builder, text string, opts docxParaOpts) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	doc.WriteString("<w:p>")
	writeDOCXRun(doc, text, opts)
	doc.WriteString("</w:p>")
}

func writeDOCXRun(doc *strings.Builder, text string, opts docxParaOpts) {
	doc.WriteString("<w:r>")
	if opts.bold || opts.sizeHalfPt > 0 || opts.colorHex != "" {
		doc.WriteString("<w:rPr>")
		if opts.bold {
			doc.WriteString("<w:b/>")
		}
		if opts.sizeHalfPt > 0 {
			doc.WriteString(fmt.Sprintf(`<w:sz w:val="%d"/>`, opts.sizeHalfPt))
		}
		if opts.colorHex != "" {
			doc.WriteString(`<w:color w:val="` + strings.ToUpper(opts.colorHex) + `"/>`)
		}
		doc.WriteString("</w:rPr>")
	}
	doc.WriteString(`<w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r>`)
}

func docxHeadingColor(accent string, style resumeTemplateStyle) string {
	if style.coloredHeadings {
		return accent
	}
	return ""
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func docxPackage(documentXML string) ([]byte, error) {
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	files := []struct {
		name string
		body string
	}{
		{name: "[Content_Types].xml", body: docxContentTypes},
		{name: "_rels/.rels", body: docxRels},
		{name: "word/document.xml", body: documentXML},
		{name: "word/styles.xml", body: docxStyles},
	}
	for _, file := range files {
		writer, err := zw.Create(file.name)
		if err != nil {
			zw.Close()
			return nil, err
		}
		if _, err := writer.Write([]byte(file.body)); err != nil {
			zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
