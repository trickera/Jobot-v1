import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../../components/Toast";
import { ResumeVersionsPanel } from "./ResumeVersionsPanel";

const apiMocks = vi.hoisted(() => ({
  listResumeVersions: vi.fn(),
}));

vi.mock("../../services/api", () => ({
  ApiError: class ApiError extends Error {},
  deleteResumeVersion: vi.fn(),
  exportResume: vi.fn(),
  listResumeVersions: apiMocks.listResumeVersions,
  renameResumeVersion: vi.fn(),
}));

describe("ResumeVersionsPanel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("loads versions by document when no job is selected", async () => {
    apiMocks.listResumeVersions.mockResolvedValueOnce({ versions: [] });

    render(
      <ToastProvider>
        <ResumeVersionsPanel documentId="doc:base" refreshToken={0} onRestore={vi.fn()} />
      </ToastProvider>,
    );

    await waitFor(() => {
      expect(apiMocks.listResumeVersions).toHaveBeenCalledWith({ jobId: undefined, documentId: "doc:base" });
    });
  });
});
