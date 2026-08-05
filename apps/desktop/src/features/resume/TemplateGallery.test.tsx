import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../../components/Toast";
import { TemplateGallery } from "./TemplateGallery";

const listResumeTemplates = vi.fn();

vi.mock("../../services/api", () => ({
  ApiError: class ApiError extends Error {},
  exportResume: vi.fn(),
  listResumeTemplates: (...args: unknown[]) => listResumeTemplates(...args),
}));

const templates = [
  { id: "template:ats-strict", name: "ATS Strict", category: "ats", engine: "native", isAts: true },
  { id: "template:ats-clean", name: "ATS Clean", category: "ats", engine: "native", isAts: true },
  { id: "template:modern-accent", name: "Modern Accent", category: "visual", engine: "native", isAts: false },
];

describe("TemplateGallery", () => {
  afterEach(() => {
    cleanup();
    listResumeTemplates.mockReset();
  });

  it("represents the three real PDF styles with visibly distinct thumbnails", async () => {
    listResumeTemplates.mockResolvedValue({ templates });
    render(
      <ToastProvider>
        <TemplateGallery templateId="template:ats-strict" onSelect={vi.fn()} canonical={null} />
      </ToastProvider>,
    );

    await screen.findByText("Modern Accent");
    expect(document.querySelector(".resume-template-thumb.is-ats-strict")).toBeTruthy();
    expect(document.querySelector(".resume-template-thumb.is-ats-clean")).toBeTruthy();
    expect(document.querySelector(".resume-template-thumb.is-modern-accent")).toBeTruthy();
  });

  it("keeps selection wired to the template id", async () => {
    const onSelect = vi.fn();
    listResumeTemplates.mockResolvedValue({ templates });
    render(
      <ToastProvider>
        <TemplateGallery templateId="template:ats-strict" onSelect={onSelect} canonical={null} />
      </ToastProvider>,
    );

    fireEvent.click(await screen.findByRole("radio", { name: /ATS Clean/ }));
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith("template:ats-clean"));
  });
});
