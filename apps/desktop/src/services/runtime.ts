export function isElectron(): boolean {
  return typeof window !== "undefined" && "senciaElectron" in window;
}
