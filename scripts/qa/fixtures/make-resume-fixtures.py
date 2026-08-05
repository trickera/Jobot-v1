"""Generates the resume fixtures the parser has never seen.

Until now the parser was exercised on exactly one resume: the author's own
two-column DevOps PDF, in Portuguese-flavoured English, from a tech role. A
parser that works on one resume is a parser that has been tested on zero, and
the failure is silent -- a resume that parses into the wrong shape still parses.

So: three resumes it has no reason to handle.

  en-two-column.pdf   A true two-column layout with a left sidebar. Text is
                      placed at explicit coordinates, which is what makes it
                      hard: naive extraction walks the page in y-order and
                      interleaves the sidebar into the middle of the job
                      history. If the parser reads across the gutter, the
                      sections come out shuffled and the name may not survive.

  intern-cv.pdf       A student with no jobs. Almost every field the parser
                      leans on for signal (years of experience, company names,
                      a headline) is absent or one line long. The failure mode
                      here is the opposite one: too little text to anchor on.

  nursing.pdf         A registered nurse. No Go, no Kubernetes, nothing the
                      keyword dictionary was tuned for. If the parser only
                      recognises a resume by its tech vocabulary, this is where
                      that shows.

Run:  py -3 scripts/qa/fixtures/make-resume-fixtures.py
Out:  scripts/qa/fixtures/resumes/*.pdf
"""

import pathlib

from fpdf import FPDF

OUT = pathlib.Path(__file__).parent / "resumes"
OUT.mkdir(parents=True, exist_ok=True)


def two_column() -> None:
    """Left sidebar + right main column, placed by coordinate, not by flow."""
    pdf = FPDF(format="letter", unit="mm")
    pdf.add_page()
    pdf.set_auto_page_break(False)

    gutter = 68.0  # where the main column starts

    # --- left sidebar -------------------------------------------------------
    pdf.set_xy(12, 18)
    pdf.set_font("Helvetica", "B", 16)
    pdf.multi_cell(50, 6, "Amara\nOkonkwo")

    pdf.set_xy(12, 34)
    pdf.set_font("Helvetica", "", 8)
    pdf.multi_cell(
        50,
        4,
        "amara.okonkwo@example.com\n+44 20 7946 0812\nLondon, United Kingdom\nlinkedin.com/in/amaraokonkwo",
    )

    pdf.set_xy(12, 56)
    pdf.set_font("Helvetica", "B", 9)
    pdf.cell(50, 5, "SKILLS")
    pdf.set_xy(12, 62)
    pdf.set_font("Helvetica", "", 8)
    pdf.multi_cell(
        50,
        4,
        "Python\nDjango\nPostgreSQL\nRedis\nDocker\nAWS\nTerraform\nGitHub Actions\npytest",
    )

    pdf.set_xy(12, 104)
    pdf.set_font("Helvetica", "B", 9)
    pdf.cell(50, 5, "LANGUAGES")
    pdf.set_xy(12, 110)
    pdf.set_font("Helvetica", "", 8)
    pdf.multi_cell(50, 4, "English (native)\nFrench (professional)\nIgbo (native)")

    pdf.set_xy(12, 128)
    pdf.set_font("Helvetica", "B", 9)
    pdf.cell(50, 5, "EDUCATION")
    pdf.set_xy(12, 134)
    pdf.set_font("Helvetica", "", 8)
    pdf.multi_cell(
        50,
        4,
        "BSc Computer Science\nUniversity of Manchester\n2014 - 2017",
    )

    # --- right main column --------------------------------------------------
    pdf.set_xy(gutter, 18)
    pdf.set_font("Helvetica", "B", 11)
    pdf.cell(0, 5, "Senior Backend Engineer")

    pdf.set_xy(gutter, 26)
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        128,
        4.5,
        "Backend engineer with eight years building payment and identity systems "
        "in Python. Led the migration of a monolith serving 40 million requests a "
        "day onto a service-per-domain architecture with no customer-visible "
        "downtime.",
    )

    pdf.set_xy(gutter, 48)
    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 5, "EXPERIENCE")

    y = 56
    jobs = [
        (
            "Senior Backend Engineer",
            "Monzo Bank, London",
            "2021 - Present",
            [
                "Owned the payments ledger service handling 40M requests/day.",
                "Cut p99 latency from 840ms to 120ms by reshaping the Postgres access path.",
                "Introduced Terraform for all payment infrastructure; removed manual provisioning.",
            ],
        ),
        (
            "Backend Engineer",
            "Deliveroo, London",
            "2018 - 2021",
            [
                "Built the rider dispatch API in Django serving 12,000 concurrent riders.",
                "Added Redis-backed caching that removed 60% of read load from Postgres.",
            ],
        ),
        (
            "Junior Software Engineer",
            "Sage Group, Newcastle",
            "2017 - 2018",
            [
                "Maintained the accounting export pipeline in Python.",
            ],
        ),
    ]
    for role, company, period, bullets in jobs:
        pdf.set_xy(gutter, y)
        pdf.set_font("Helvetica", "B", 9)
        pdf.cell(0, 4.5, role)
        y += 4.5
        pdf.set_xy(gutter, y)
        pdf.set_font("Helvetica", "I", 8)
        pdf.cell(0, 4, f"{company}  |  {period}")
        y += 5
        pdf.set_font("Helvetica", "", 8)
        for bullet in bullets:
            pdf.set_xy(gutter, y)
            pdf.multi_cell(128, 4, f"- {bullet}")
            y = pdf.get_y() + 0.5
        y += 3

    pdf.output(str(OUT / "en-two-column.pdf"))


def intern_cv() -> None:
    """A student. No jobs, no headline, barely any text to anchor on."""
    pdf = FPDF(format="letter", unit="mm")
    pdf.add_page()
    pdf.set_auto_page_break(True, margin=15)

    pdf.set_font("Helvetica", "B", 15)
    pdf.cell(0, 8, "Lucas Meyer", new_x="LMARGIN", new_y="NEXT")

    pdf.set_font("Helvetica", "", 9)
    pdf.cell(
        0,
        5,
        "lucas.meyer@example.com | +49 30 5555 0198 | Berlin, Germany",
        new_x="LMARGIN",
        new_y="NEXT",
    )
    pdf.ln(4)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "EDUCATION", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        0,
        4.5,
        "BSc Information Systems (in progress, expected 2027)\n"
        "Technische Universitaet Berlin\n"
        "Relevant coursework: Algorithms, Databases, Operating Systems",
    )
    pdf.ln(3)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "PROJECTS", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        0,
        4.5,
        "Course scheduling tool (2025)\n"
        "- Built a small web app in JavaScript that lets students compare timetables.\n"
        "- Used by roughly 200 students in my faculty.\n\n"
        "Weather bot (2024)\n"
        "- A Telegram bot in Python that posts a daily forecast.",
    )
    pdf.ln(3)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "SKILLS", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(0, 4.5, "Python, JavaScript, HTML, CSS, Git, SQL (basic)")
    pdf.ln(3)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "LANGUAGES", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(0, 4.5, "German (native), English (fluent)")

    pdf.output(str(OUT / "intern-cv.pdf"))


def nursing() -> None:
    """A registered nurse. Nothing the tech keyword dictionary was tuned for."""
    pdf = FPDF(format="letter", unit="mm")
    pdf.add_page()
    pdf.set_auto_page_break(True, margin=15)

    pdf.set_font("Helvetica", "B", 15)
    pdf.cell(0, 8, "Priya Raghunathan, RN, BSN", new_x="LMARGIN", new_y="NEXT")

    pdf.set_font("Helvetica", "", 9)
    pdf.cell(
        0,
        5,
        "priya.raghunathan@example.com | (312) 555-0147 | Chicago, IL",
        new_x="LMARGIN",
        new_y="NEXT",
    )
    pdf.ln(4)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "SUMMARY", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        0,
        4.5,
        "Registered nurse with nine years in acute care, six of them in a Level I "
        "trauma intensive care unit. Charge nurse for a 24-bed unit. Precepted "
        "fourteen new graduate nurses.",
    )
    pdf.ln(3)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "CLINICAL EXPERIENCE", new_x="LMARGIN", new_y="NEXT")

    entries = [
        (
            "Charge Nurse, Medical Intensive Care Unit",
            "Northwestern Memorial Hospital, Chicago, IL",
            "2020 - Present",
            [
                "Coordinate staffing and patient assignments for a 24-bed ICU across night shift.",
                "Manage ventilated patients, vasoactive drips, and continuous renal replacement therapy.",
                "Reduced central line infection rate by 38% by rewriting the unit's line care checklist.",
            ],
        ),
        (
            "Staff Nurse, Emergency Department",
            "Rush University Medical Center, Chicago, IL",
            "2017 - 2020",
            [
                "Triaged patients in a Level I trauma center seeing 300+ arrivals per day.",
                "Certified in ACLS, PALS, and TNCC.",
            ],
        ),
        (
            "Staff Nurse, Medical-Surgical",
            "Advocate Illinois Masonic, Chicago, IL",
            "2016 - 2017",
            [
                "Cared for post-operative patients on a 32-bed medical-surgical floor.",
            ],
        ),
    ]
    for role, employer, period, bullets in entries:
        pdf.set_font("Helvetica", "B", 9)
        pdf.cell(0, 5, role, new_x="LMARGIN", new_y="NEXT")
        pdf.set_font("Helvetica", "I", 8)
        pdf.cell(0, 4.5, f"{employer}  |  {period}", new_x="LMARGIN", new_y="NEXT")
        pdf.set_font("Helvetica", "", 8.5)
        for bullet in bullets:
            # new_x=LMARGIN: multi_cell otherwise leaves the cursor at the right
            # edge, so the next bullet would be handed a zero-width column.
            pdf.multi_cell(0, 4.2, f"- {bullet}", new_x="LMARGIN", new_y="NEXT")
        pdf.ln(2)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "LICENSES AND CERTIFICATIONS", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        0,
        4.5,
        "Registered Nurse, State of Illinois (License #041-xxxxxx)\n"
        "CCRN - Critical Care Registered Nurse, AACN\n"
        "ACLS, BLS, PALS, TNCC - American Heart Association",
    )
    pdf.ln(2)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "EDUCATION", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        0,
        4.5,
        "Bachelor of Science in Nursing (BSN)\nUniversity of Illinois Chicago, 2012 - 2016",
    )
    pdf.ln(2)

    pdf.set_font("Helvetica", "B", 10)
    pdf.cell(0, 6, "SKILLS", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(
        0,
        4.5,
        "Critical care, patient assessment, ventilator management, medication administration, "
        "electronic health records (Epic), care coordination, patient education, preceptorship",
    )

    pdf.output(str(OUT / "nursing.pdf"))


if __name__ == "__main__":
    two_column()
    intern_cv()
    nursing()
    for pdf_file in sorted(OUT.glob("*.pdf")):
        print(f"{pdf_file.stat().st_size:>8} bytes  {pdf_file.name}")
