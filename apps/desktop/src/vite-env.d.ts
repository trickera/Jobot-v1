/// <reference types="vite/client" />

export interface SenciaElectronApi {
  getApiToken(): Promise<string>;
  saveBinaryFile(o: {
    fileName: string;
    base64: string;
    filter: { name: string; extensions: string[] };
  }): Promise<{ path?: string; canceled: boolean }>;
  saveTextFile(o: {
    fileName: string;
    content: string;
    filter: { name: string; extensions: string[] };
  }): Promise<{ path?: string; canceled: boolean }>;
  openPath(path: string): Promise<void>;
  showItemInFolder(path: string): Promise<void>;
  openAppDataFolder(): Promise<void>;
  windowControl(action: "minimize" | "maximize" | "close"): void;
  getBootLogs(): Promise<string[]>;
  notifyJob(o: { title: string; body: string; url: string }): Promise<boolean>;
}

declare global {
  interface Window {
    senciaElectron?: SenciaElectronApi;
  }
}
