import type { ToastHandle } from "../../components/Toast";

const ELAPSED_AFTER_MS = 5000;

/**
 * Wraps a loading toast with sub-step labels and an elapsed-time hint so long
 * AI calls never look frozen (RS-UX-01 / RS-PERF-01). The elapsed counter is a
 * frontend timer: it signals honest progress perception, not backend state.
 */
export function createStepToast(
  push: (title: string) => ToastHandle,
  initialTitle: string,
  hint = "A large resume or a busy provider can take a little longer.",
) {
  const pending = push(initialTitle);
  const startedAt = Date.now();
  let title = initialTitle;
  const timer = window.setInterval(() => {
    const elapsedMs = Date.now() - startedAt;
    if (elapsedMs >= ELAPSED_AFTER_MS) {
      pending.update({
        tone: "loading",
        title,
        description: `Still working… ${Math.floor(elapsedMs / 1000)}s · ${hint}`,
      });
    }
  }, 1000);
  const stop = () => window.clearInterval(timer);
  return {
    step(nextTitle: string) {
      title = nextTitle;
      pending.update({ tone: "loading", title: nextTitle });
    },
    success(successTitle: string, description?: string) {
      stop();
      pending.update({ tone: "success", title: successTitle, description });
    },
    error(errorTitle: string, description?: string) {
      stop();
      pending.update({ tone: "error", title: errorTitle, description });
    },
  };
}
