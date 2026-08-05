import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Titlebar } from "./Titlebar";

const windowControl = vi.fn();

describe("Titlebar", () => {
  beforeEach(() => {
    windowControl.mockReset();
    window.localStorage.clear();
    document.documentElement.dataset.theme = "dark";
    document.documentElement.classList.remove("is-theme-changing");
    Object.defineProperty(window, "senciaElectron", {
      configurable: true,
      value: { windowControl } as unknown as Window["senciaElectron"],
    });
  });

  afterEach(cleanup);

  it("shows the renderer brand and keeps readiness in the prominent status dot", () => {
    render(<Titlebar online />);

    expect(screen.getByText("JoBot")).toBeTruthy();
    expect(screen.queryByText("Pronto")).toBeNull();
    expect(screen.getByRole("status", { name: "Pronto" }).classList.contains("is-online")).toBe(true);
  });

  it("keeps busy and offline states accessible without restoring the centered label", () => {
    const { rerender } = render(<Titlebar online busy />);
    expect(screen.getByRole("status", { name: "Buscando..." }).classList.contains("is-busy")).toBe(true);

    rerender(<Titlebar online={false} />);
    expect(screen.getByRole("status", { name: "Servico offline" }).classList.contains("is-online")).toBe(false);
  });

  it("persists the theme and preserves all native window controls", () => {
    render(<Titlebar online />);

    fireEvent.click(screen.getByRole("button", { name: "Usar tema claro" }));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(window.localStorage.getItem("sencia-theme")).toBe("light");

    fireEvent.click(screen.getByRole("button", { name: "Minimizar" }));
    fireEvent.click(screen.getByRole("button", { name: "Maximizar" }));
    fireEvent.click(screen.getByRole("button", { name: "Fechar" }));
    expect(windowControl.mock.calls.map(([action]) => action)).toEqual(["minimize", "maximize", "close"]);
  });
});
