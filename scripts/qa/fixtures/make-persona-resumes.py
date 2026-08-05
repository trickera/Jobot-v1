"""Generates the five persona resumes (one per career area) as PDFs, so the
usability tester can Upload a real file the way a common user does, not just
paste text. Content mirrors docs/qa/usability-test-protocol.md.

Run:  py -3 scripts/qa/fixtures/make-persona-resumes.py
Out:  scripts/qa/fixtures/personas/*.pdf
"""

import pathlib

from fpdf import FPDF

OUT = pathlib.Path(__file__).parent / "personas"
OUT.mkdir(parents=True, exist_ok=True)


def resume(filename: str, name: str, contact: str, sections: list[tuple[str, list[str]]]) -> None:
    pdf = FPDF(format="letter", unit="mm")
    pdf.add_page()
    pdf.set_auto_page_break(True, margin=16)

    pdf.set_font("Helvetica", "B", 15)
    pdf.multi_cell(0, 8, name, new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("Helvetica", "", 9)
    pdf.multi_cell(0, 5, contact, new_x="LMARGIN", new_y="NEXT")
    pdf.ln(3)

    for heading, lines in sections:
        pdf.set_font("Helvetica", "B", 10)
        pdf.multi_cell(0, 6, heading.upper(), new_x="LMARGIN", new_y="NEXT")
        pdf.set_font("Helvetica", "", 9)
        for line in lines:
            pdf.multi_cell(0, 4.6, line, new_x="LMARGIN", new_y="NEXT")
        pdf.ln(2.5)

    pdf.output(str(OUT / filename))


resume(
    "1-software-backend.pdf",
    "Rafael Moreira",
    "rafael.moreira@example.com | +55 11 98888-1234 | Sao Paulo, Brazil | linkedin.com/in/rafaelmoreira",
    [
        ("Summary", [
            "Backend engineer with 6 years building payment and API platforms in Go and Python.",
            "Comfortable owning services end to end, from database to production.",
        ]),
        ("Experience", [
            "Senior Backend Engineer - Nubank, Sao Paulo (2021 - Present)",
            "- Built and operated the transaction ledger service in Go, handling 25M requests/day.",
            "- Cut p99 latency from 600ms to 90ms by reshaping the PostgreSQL access path.",
            "- Introduced Terraform for all payment infrastructure.",
            "Backend Engineer - iFood, Sao Paulo (2018 - 2021)",
            "- Built the order dispatch API in Python (Django) serving 8,000 concurrent drivers.",
            "- Added Redis caching that removed 55% of read load from PostgreSQL.",
        ]),
        ("Education", [
            "BSc Computer Science - Universidade de Sao Paulo (2014 - 2018)",
        ]),
        ("Skills", [
            "Go, Python, PostgreSQL, Redis, Docker, Kubernetes, AWS, Terraform, REST, gRPC, Git",
        ]),
    ],
)

resume(
    "2-nursing.pdf",
    "Priya Raghunathan, RN, BSN",
    "priya.raghunathan@example.com | (312) 555-0147 | Chicago, IL",
    [
        ("Summary", [
            "Registered nurse with nine years in acute care, six of them in a Level I trauma ICU.",
            "Charge nurse for a 24-bed unit. Precepted fourteen new graduate nurses.",
        ]),
        ("Clinical Experience", [
            "Charge Nurse, Medical Intensive Care Unit - Northwestern Memorial Hospital, Chicago (2020 - Present)",
            "- Coordinate staffing and patient assignments for a 24-bed ICU on night shift.",
            "- Manage ventilated patients, vasoactive drips, and continuous renal replacement therapy.",
            "- Reduced central line infection rate by 38% by rewriting the unit's line-care checklist.",
            "Staff Nurse, Emergency Department - Rush University Medical Center, Chicago (2017 - 2020)",
            "- Triaged patients in a Level I trauma center seeing 300+ arrivals per day.",
        ]),
        ("Licenses and Certifications", [
            "Registered Nurse, State of Illinois; CCRN; ACLS, BLS, PALS, TNCC",
        ]),
        ("Education", [
            "Bachelor of Science in Nursing (BSN) - University of Illinois Chicago (2012 - 2016)",
        ]),
        ("Skills", [
            "Critical care, patient assessment, ventilator management, medication administration,",
            "electronic health records (Epic), care coordination, patient education, preceptorship",
        ]),
    ],
)

resume(
    "3-finance.pdf",
    "Elena Kovacs",
    "elena.kovacs@example.com | +49 30 5555 0198 | Berlin, Germany",
    [
        ("Summary", [
            "Financial analyst with 7 years in FP&A and corporate finance across SaaS and manufacturing.",
            "Owns the monthly close, budgeting, and board reporting.",
        ]),
        ("Experience", [
            "Senior Financial Analyst - Delivery Hero, Berlin (2020 - Present)",
            "- Own the FP&A model for a 400M EUR business unit; run monthly close and variance analysis.",
            "- Built a rolling 18-month forecast in Excel/Anaplan adopted company-wide.",
            "- Cut the close cycle from 9 to 5 days.",
            "Financial Analyst - Siemens, Munich (2016 - 2020)",
            "- Prepared board reporting packs and capex business cases.",
            "- Supported the annual budget for a manufacturing division (1.2B EUR revenue).",
        ]),
        ("Education", [
            "MSc Finance - Frankfurt School of Finance & Management (2014 - 2016)",
            "BSc Business Administration - University of Vienna (2011 - 2014)",
        ]),
        ("Skills", [
            "FP&A, financial modeling, budgeting, forecasting, variance analysis, Excel, Anaplan,",
            "SAP, IFRS, board reporting, capex, cash flow, valuation",
        ]),
    ],
)

resume(
    "4-marketing.pdf",
    "Marcus Bennett",
    "marcus.bennett@example.com | +44 20 7946 0812 | London, United Kingdom",
    [
        ("Summary", [
            "Digital marketing manager with 8 years in performance and lifecycle marketing for",
            "DTC and B2B SaaS. Runs paid acquisition, SEO, and email, and owns the CAC/LTV number.",
        ]),
        ("Experience", [
            "Digital Marketing Manager - Gymshark, London (2021 - Present)",
            "- Own a 6M GBP/year paid media budget across Meta, Google, and TikTok.",
            "- Reduced blended CAC by 27% while scaling spend 40% through creative testing.",
            "- Rebuilt the email lifecycle program (Klaviyo), lifting repeat-purchase rate by 18%.",
            "Performance Marketing Specialist - Monzo, London (2017 - 2021)",
            "- Ran Google Ads and paid social for current-account acquisition.",
            "- Built attribution dashboards in GA4 and Looker.",
        ]),
        ("Education", [
            "BA Marketing - University of Manchester (2013 - 2017)",
        ]),
        ("Skills", [
            "Paid media, Meta Ads, Google Ads, TikTok Ads, SEO, email marketing, Klaviyo, GA4,",
            "Looker, A/B testing, CRO, attribution, CAC/LTV, marketing analytics",
        ]),
    ],
)

resume(
    "5-product-design.pdf",
    "Sofia Almeida",
    "sofia.almeida@example.com | +351 91 234 5678 | Lisbon, Portugal | sofiaalmeida.design",
    [
        ("Summary", [
            "Product designer with 6 years shipping end-to-end product experiences for fintech",
            "and marketplace apps. Owns research, interaction, and design systems.",
        ]),
        ("Experience", [
            "Senior Product Designer - Revolut, Lisbon (2021 - Present)",
            "- Led design for the savings and investments product used by 4M customers.",
            "- Built and maintained the mobile design system in Figma adopted by 30+ designers.",
            "- Ran usability studies that reshaped the onboarding flow, lifting completion 22%.",
            "Product Designer - OLX Group, Lisbon (2018 - 2021)",
            "- Designed seller tools for a marketplace with 20M monthly users.",
            "- Partnered with researchers on generative and evaluative studies.",
        ]),
        ("Education", [
            "BA Communication Design - Faculdade de Belas-Artes, Lisbon (2014 - 2018)",
        ]),
        ("Skills", [
            "Product design, UX research, interaction design, Figma, design systems, prototyping,",
            "usability testing, user flows, wireframing, accessibility, mobile design, Design Ops",
        ]),
    ],
)

for pdf_file in sorted(OUT.glob("*.pdf")):
    print(f"{pdf_file.stat().st_size:>7} bytes  {pdf_file.name}")
