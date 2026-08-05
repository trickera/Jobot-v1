import { contextBridge, ipcRenderer } from "electron";

type SaveResult = { path?: string; canceled: boolean };

const senciaElectron = {
  getApiToken: (): Promise<string> => ipcRenderer.invoke("sencia:getApiToken"),

  getBootLogs: (): Promise<string[]> => ipcRenderer.invoke("sencia:getBootLogs"),

  saveBinaryFile: (options: {
    fileName: string;
    base64: string;
    filter: { name: string; extensions: string[] };
  }): Promise<SaveResult> => ipcRenderer.invoke("sencia:saveBinaryFile", options),

  saveTextFile: (options: {
    fileName: string;
    content: string;
    filter: { name: string; extensions: string[] };
  }): Promise<SaveResult> => ipcRenderer.invoke("sencia:saveTextFile", options),

  openPath: (path: string): Promise<void> => ipcRenderer.invoke("sencia:openPath", path),

  showItemInFolder: (path: string): Promise<void> =>
    ipcRenderer.invoke("sencia:showItemInFolder", path),

  openAppDataFolder: (): Promise<void> => ipcRenderer.invoke("sencia:openAppDataFolder"),

  notifyJob: (options: {
    title: string;
    body: string;
    url: string;
  }): Promise<boolean> => ipcRenderer.invoke("sencia:notifyJob", options),

  windowControl: (action: "minimize" | "maximize" | "close"): void => {
    ipcRenderer.send("sencia:windowControl", action);
  },
};

contextBridge.exposeInMainWorld("senciaElectron", senciaElectron);
