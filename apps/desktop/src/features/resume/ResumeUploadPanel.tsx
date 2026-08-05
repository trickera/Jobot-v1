import { FileText, LoaderCircle, UploadCloud } from "lucide-react";
import { useRef, useState, type DragEvent } from "react";

const ACCEPTED_EXTENSIONS = [".pdf", ".docx", ".txt"];
const ACCEPT_ATTR = ".pdf,.docx,.txt";

function hasAcceptedExtension(name: string): boolean {
  const lower = name.toLowerCase();
  return ACCEPTED_EXTENSIONS.some((ext) => lower.endsWith(ext));
}

type ResumeUploadPanelProps = {
  busy: boolean;
  fileName?: string | null;
  onFile: (file: File) => void;
  onReject: (message: string) => void;
};

export function ResumeUploadPanel({ busy, fileName, onFile, onReject }: ResumeUploadPanelProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);

  function handleFiles(files: FileList | null) {
    const file = files?.[0];
    if (!file) return;
    if (!hasAcceptedExtension(file.name)) {
      onReject("Unsupported file type. Please upload a PDF, DOCX or TXT resume.");
      return;
    }
    onFile(file);
  }

  function openPicker() {
    if (!busy) inputRef.current?.click();
  }

  function onDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragging(false);
    if (busy) return;
    handleFiles(event.dataTransfer.files);
  }

  function onDragOver(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    if (!busy) setDragging(true);
  }

  return (
    <div className="resume-upload">
      <div
        className={`resume-dropzone${dragging ? " is-dragging" : ""}${busy ? " is-busy" : ""}`}
        role="button"
        tabIndex={0}
        aria-label="Upload a resume file (PDF, DOCX or TXT)"
        aria-disabled={busy}
        onClick={openPicker}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            openPicker();
          }
        }}
        onDrop={onDrop}
        onDragOver={onDragOver}
        onDragLeave={() => setDragging(false)}
      >
        <span className="resume-dropzone-icon" aria-hidden="true">
          {busy ? <LoaderCircle size={26} className="is-spinning" /> : <UploadCloud size={26} />}
        </span>
        <p className="resume-dropzone-title">
          {busy ? "Reading your file…" : "Drag & drop your resume, or click to browse"}
        </p>
        <p className="resume-dropzone-hint">PDF, DOCX or TXT · up to 8 MB</p>
        <input
          ref={inputRef}
          type="file"
          accept={ACCEPT_ATTR}
          className="resume-file-input"
          onChange={(event) => {
            handleFiles(event.target.files);
            // Clear the input so picking the SAME file again re-fires
            // onChange (e.g. after "Choose another file").
            event.target.value = "";
          }}
          tabIndex={-1}
        />
      </div>
      {fileName ? (
        <p className="resume-upload-file">
          <FileText size={14} aria-hidden="true" />
          <span>{fileName}</span>
        </p>
      ) : null}
    </div>
  );
}
