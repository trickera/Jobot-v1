import { AlertCircle, CheckCircle2, Info, LoaderCircle, X } from "lucide-react";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

export type ToastTone = "success" | "error" | "info" | "loading";

export type ToastOptions = {
  title: string;
  description?: string;
  tone?: ToastTone;
  /** Auto-dismiss delay in ms. Use 0 to keep it until dismissed (e.g. loading). */
  duration?: number;
};

type Toast = ToastOptions & {
  id: number;
  tone: ToastTone;
};

export type ToastHandle = {
  id: number;
  update: (next: ToastOptions) => void;
  dismiss: () => void;
};

type ToastContextValue = {
  push: (options: ToastOptions) => ToastHandle;
  success: (title: string, description?: string) => ToastHandle;
  error: (title: string, description?: string) => ToastHandle;
  info: (title: string, description?: string) => ToastHandle;
  loading: (title: string, description?: string) => ToastHandle;
  dismiss: (id: number) => void;
};

const ToastContext = createContext<ToastContextValue | null>(null);

const DEFAULT_DURATION = 4200;
const TONE_ICON: Record<ToastTone, typeof CheckCircle2> = {
  success: CheckCircle2,
  error: AlertCircle,
  info: Info,
  loading: LoaderCircle,
};
const TONE_ROLE: Record<ToastTone, "status" | "alert"> = {
  success: "status",
  error: "alert",
  info: "status",
  loading: "status",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const idRef = useRef(0);
  const timers = useRef(new Map<number, number>());

  const clearTimer = useCallback((id: number) => {
    const timer = timers.current.get(id);
    if (timer) {
      window.clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const dismiss = useCallback(
    (id: number) => {
      clearTimer(id);
      setToasts((current) => current.filter((toast) => toast.id !== id));
    },
    [clearTimer],
  );

  const scheduleDismiss = useCallback(
    (id: number, duration: number) => {
      clearTimer(id);
      if (duration > 0) {
        timers.current.set(
          id,
          window.setTimeout(() => dismiss(id), duration),
        );
      }
    },
    [clearTimer, dismiss],
  );

  const push = useCallback(
    (options: ToastOptions): ToastHandle => {
      idRef.current += 1;
      const id = idRef.current;
      const tone = options.tone ?? "info";
      const duration = options.duration ?? (tone === "loading" ? 0 : DEFAULT_DURATION);
      const toast: Toast = { ...options, id, tone };
      setToasts((current) => [...current, toast].slice(-4));
      scheduleDismiss(id, duration);
      return {
        id,
        update: (next) => {
          const nextTone = next.tone ?? "info";
          const nextDuration = next.duration ?? (nextTone === "loading" ? 0 : DEFAULT_DURATION);
          setToasts((current) =>
            current.map((item) => (item.id === id ? { ...item, ...next, id, tone: nextTone } : item)),
          );
          scheduleDismiss(id, nextDuration);
        },
        dismiss: () => dismiss(id),
      };
    },
    [dismiss, scheduleDismiss],
  );

  useEffect(() => {
    const map = timers.current;
    return () => {
      map.forEach((timer) => window.clearTimeout(timer));
      map.clear();
    };
  }, []);

  const value = useMemo<ToastContextValue>(
    () => ({
      push,
      success: (title, description) => push({ title, description, tone: "success" }),
      error: (title, description) => push({ title, description, tone: "error", duration: 6000 }),
      info: (title, description) => push({ title, description, tone: "info" }),
      loading: (title, description) => push({ title, description, tone: "loading" }),
      dismiss,
    }),
    [push, dismiss],
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <Toaster toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

function Toaster({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }) {
  if (typeof document === "undefined") return null;
  return createPortal(
    <div className="toast-viewport" role="region" aria-label="Notifications" aria-live="polite">
      {toasts.map((toast) => {
        const Icon = TONE_ICON[toast.tone];
        return (
          <div key={toast.id} className={`toast toast-${toast.tone}`} role={TONE_ROLE[toast.tone]}>
            <span className="toast-icon" aria-hidden="true">
              <Icon size={18} className={toast.tone === "loading" ? "is-spinning" : undefined} />
            </span>
            <div className="toast-body">
              <p className="toast-title">{toast.title}</p>
              {toast.description ? <p className="toast-description">{toast.description}</p> : null}
            </div>
            <button
              type="button"
              className="toast-close"
              aria-label="Dismiss notification"
              onClick={() => onDismiss(toast.id)}
            >
              <X size={15} />
            </button>
          </div>
        );
      })}
    </div>,
    document.body,
  );
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error("useToast must be used within a ToastProvider");
  }
  return context;
}
