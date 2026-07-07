import { useEffect } from "react";

interface SavedEvent {
  key: string;
  status: number;
}

interface ErrorEvent {
  message: string;
}

interface OnlyOfficeEditorProps {
  documentId: string;
  gatewayUrl: string;
  token?: string;
  mode?: "edit" | "view";
  onReady?: () => void;
  onSaved?: (event: SavedEvent) => void;
  onError?: (event: ErrorEvent) => void;
  style?: React.CSSProperties;
}

export function OnlyOfficeEditor({
  documentId,
  gatewayUrl,
  token,
  mode,
  onReady,
  onSaved,
  onError,
  style,
}: OnlyOfficeEditorProps) {
  useEffect(() => {
    const handler = (event: MessageEvent) => {
      try {
        const msg = JSON.parse(event.data);
        switch (msg.type) {
          case "onlyoffice:ready":
            onReady?.();
            break;
          case "onlyoffice:saved":
            onSaved?.(msg.data);
            break;
          case "onlyoffice:error":
            onError?.(msg.data);
            break;
        }
      } catch {
        // Ignore non-JSON messages
      }
    };

    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, [onReady, onSaved, onError]);

  const params = new URLSearchParams({ document_id: documentId });
  if (token) params.set("token", token);
  if (mode && mode !== "edit") params.set("mode", mode);

  return (
    <iframe
      src={`${gatewayUrl}/edit?${params.toString()}`}
      style={{
        width: "100%",
        height: "600px",
        border: "none",
        ...style,
      }}
      key={`${documentId}-${token || ""}-${mode || "edit"}`}
      title="ONLYOFFICE Editor"
    />
  );
}

export type { SavedEvent, ErrorEvent, OnlyOfficeEditorProps };

export type OnlyOfficeEditorMode = "edit" | "view";

export const VERSION = "0.1.0";
