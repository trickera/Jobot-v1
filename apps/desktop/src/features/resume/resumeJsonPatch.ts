import type { CanonicalResume, JsonPatchOp } from "../../types";

function unescapeToken(token: string): string {
  return token.replace(/~1/g, "/").replace(/~0/g, "~");
}

function parsePointer(pointer: string): string[] {
  if (pointer === "" || pointer === "/") return [];
  return pointer.split("/").slice(1).map(unescapeToken);
}

// getValueAtPointer resolves the current value at an RFC 6901 JSON Pointer on
// the base resume, so the diff view can show "before → after" for a patch.
// Returns undefined for an add at "/-" or any path that doesn't resolve.
export function getValueAtPointer(base: CanonicalResume, pointer: string): unknown {
  const tokens = parsePointer(pointer);
  let current: unknown = base;
  for (const token of tokens) {
    if (current == null) return undefined;
    if (Array.isArray(current)) {
      const index = token === "-" ? current.length : Number(token);
      current = Number.isNaN(index) ? undefined : current[index];
    } else if (typeof current === "object") {
      current = (current as Record<string, unknown>)[token];
    } else {
      return undefined;
    }
  }
  return current;
}

export type PatchApplication = {
  resume: CanonicalResume;
  // Indexes (into the patches argument) of operations that did not resolve
  // against the document — mirroring where the backend's RFC 6902 library
  // (evanphx/json-patch) would have errored. They are skipped, but callers
  // MUST surface them: a silently-vanishing accepted change is how the saved
  // resume diverges from what the user reviewed.
  failedIndexes: number[];
};

// applyResumePatchesDetailed applies RFC 6902 add/replace/remove operations
// onto a cloned copy of the base resume. Used to compute "final = base + the
// patches the user kept checked" client-side, without another backend
// round-trip every time a checkbox in ResumeDiffPreview is toggled.
// Failure semantics deliberately track evanphx/json-patch (the backend's
// applier): an op whose parent path is missing, whose array index is out of
// range, or that removes/replaces something that isn't there is a FAILURE,
// not a silent no-op.
export function applyResumePatchesDetailed(base: CanonicalResume, patches: JsonPatchOp[]): PatchApplication {
  const root = structuredClone(base) as Record<string, unknown>;
  const failedIndexes: number[] = [];

  patches.forEach((op, patchIndex) => {
    const fail = () => failedIndexes.push(patchIndex);
    const tokens = parsePointer(op.path);
    if (tokens.length === 0) return fail();

    let parent: unknown = root;
    for (let i = 0; i < tokens.length - 1; i += 1) {
      if (parent == null || typeof parent !== "object") return fail();
      if (Array.isArray(parent)) {
        const step = Number(tokens[i]);
        if (Number.isNaN(step) || step < 0 || step >= parent.length) return fail();
        parent = parent[step];
      } else {
        parent = (parent as Record<string, unknown>)[tokens[i]];
      }
    }
    if (parent == null || typeof parent !== "object") return fail();
    const key = tokens[tokens.length - 1];

    if (Array.isArray(parent)) {
      const index = key === "-" ? parent.length : Number(key);
      if (Number.isNaN(index) || index < 0) return fail();
      if (op.op === "add") {
        if (index > parent.length) return fail();
        parent.splice(index, 0, op.value);
      } else if (op.op === "replace") {
        if (index >= parent.length) return fail();
        parent[index] = op.value;
      } else if (op.op === "remove") {
        if (index >= parent.length) return fail();
        parent.splice(index, 1);
      } else {
        return fail();
      }
    } else {
      const record = parent as Record<string, unknown>;
      if (op.op === "add") {
        record[key] = op.value;
      } else if (op.op === "replace") {
        if (!(key in record)) return fail();
        record[key] = op.value;
      } else if (op.op === "remove") {
        if (!(key in record)) return fail();
        delete record[key];
      } else {
        return fail();
      }
    }
  });

  return { resume: root as unknown as CanonicalResume, failedIndexes };
}
