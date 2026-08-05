import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LogEntry } from "../../types";
import { LogsView } from "./LogsView";

const apiMocks = vi.hoisted(() => ({
  loadLogs: vi.fn(),
}));

vi.mock("../../services/api", () => ({
  ApiError: class ApiError extends Error {},
  ...apiMocks,
}));

const log: LogEntry = {
  id: 1,
  ts: "2026-07-15T03:00:00Z",
  level: "success",
  message: "Busca concluida com 4 vagas",
};

describe("LogsView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("shows a real loading state instead of flashing the empty state", async () => {
    let resolveRequest!: (value: { logs: LogEntry[] }) => void;
    apiMocks.loadLogs.mockReturnValue(new Promise((resolve) => { resolveRequest = resolve; }));
    render(<LogsView />);

    expect(screen.getByText("Carregando atividade...")).not.toBeNull();
    expect(screen.queryByText("Nenhum log ainda")).toBeNull();

    await act(async () => resolveRequest({ logs: [] }));
    expect(await screen.findByText("Nenhum log ainda")).not.toBeNull();
  });

  it("renders backend log order and keeps the exact message as text", async () => {
    const second = { ...log, id: 2, level: "warning" as const, message: "Fonte temporariamente indisponivel" };
    apiMocks.loadLogs.mockResolvedValue({ logs: [log, second] });
    render(<LogsView />);

    const lines = await screen.findAllByText(/Busca concluida|Fonte temporariamente/);
    expect(lines.map((line) => line.textContent)).toEqual([log.message, second.message]);
    expect(screen.getByRole("status").textContent).toContain("2 eventos");
    expect(screen.getByText(log.message).closest(".precision-log-console")?.getAttribute("aria-live")).toBeNull();
  });

  it("keeps previous logs on refresh failure and exposes an accessible error", async () => {
    apiMocks.loadLogs.mockResolvedValueOnce({ logs: [log] }).mockRejectedValueOnce(new Error("backend offline"));
    render(<LogsView />);

    expect(await screen.findByText(log.message)).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Atualizar" }));

    expect(await screen.findByRole("alert")).not.toBeNull();
    expect(screen.getByText(log.message)).not.toBeNull();
    expect(screen.queryByText("Nenhum log ainda")).toBeNull();
  });

  it("polls every three seconds and cleans the interval on unmount", async () => {
    vi.useFakeTimers();
    apiMocks.loadLogs.mockResolvedValue({ logs: [log] });
    const clearSpy = vi.spyOn(window, "clearInterval");
    const view = render(<LogsView />);

    await act(async () => Promise.resolve());
    expect(apiMocks.loadLogs).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(apiMocks.loadLogs).toHaveBeenCalledTimes(2);

    view.unmount();
    expect(clearSpy).toHaveBeenCalledTimes(1);
    clearSpy.mockRestore();
  });

  it("does not claim the log is empty when the first request fails", async () => {
    apiMocks.loadLogs.mockRejectedValue(new Error("backend offline"));
    render(<LogsView />);

    expect(await screen.findByRole("alert")).not.toBeNull();
    expect(screen.getByText("Atividade indisponivel")).not.toBeNull();
    expect(screen.queryByText("Nenhum log ainda")).toBeNull();
  });
});
