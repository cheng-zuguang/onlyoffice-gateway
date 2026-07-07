import { render, act } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { OnlyOfficeEditor } from "./OnlyOfficeEditor";

describe("OnlyOfficeEditor", () => {
  // S16: Renders an iframe pointing to the Gateway /edit endpoint
  it("renders an iframe with correct src", () => {
    const { container } = render(
      <OnlyOfficeEditor documentId="doc-123" gatewayUrl="https://gateway.example.com" />
    );

    const iframe = container.querySelector("iframe");
    expect(iframe).not.toBeNull();
    expect(iframe!.src).toBe("https://gateway.example.com/edit?document_id=doc-123");
    expect(iframe!.style.width).toBe("100%");
    expect(iframe!.style.height).toBe("600px");
  });

  // S17: postMessage "onlyoffice:ready" triggers onReady
  it("calls onReady when editor posts ready message", () => {
    const onReady = vi.fn();
    render(
      <OnlyOfficeEditor
        documentId="doc-123"
        gatewayUrl="https://gateway.example.com"
        onReady={onReady}
      />
    );

    act(() => {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ type: "onlyoffice:ready" }),
        })
      );
    });

    expect(onReady).toHaveBeenCalledTimes(1);
  });

  // S18: postMessage "onlyoffice:saved" triggers onSaved
  it("calls onSaved when editor posts saved message", () => {
    const onSaved = vi.fn();
    const eventData = { key: "doc-123", status: 2 };
    render(
      <OnlyOfficeEditor
        documentId="doc-123"
        gatewayUrl="https://gateway.example.com"
        onSaved={onSaved}
      />
    );

    act(() => {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ type: "onlyoffice:saved", data: eventData }),
        })
      );
    });

    expect(onSaved).toHaveBeenCalledWith(eventData);
  });

  // S19: postMessage "onlyoffice:error" triggers onError
  it("calls onError when editor posts error message", () => {
    const onError = vi.fn();
    const errData = { message: "failed to load" };
    render(
      <OnlyOfficeEditor
        documentId="doc-123"
        gatewayUrl="https://gateway.example.com"
        onError={onError}
      />
    );

    act(() => {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ type: "onlyoffice:error", data: errData }),
        })
      );
    });

    expect(onError).toHaveBeenCalledWith(errData);
  });

  // Ignores unknown message types
  it("ignores unknown postMessage types", () => {
    const onReady = vi.fn();
    const onSaved = vi.fn();
    const onError = vi.fn();
    render(
      <OnlyOfficeEditor
        documentId="doc-123"
        gatewayUrl="https://gateway.example.com"
        onReady={onReady}
        onSaved={onSaved}
        onError={onError}
      />
    );

    act(() => {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ type: "completely.unknown" }),
        })
      );
    });

    expect(onReady).not.toHaveBeenCalled();
    expect(onSaved).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });

  // Phase 1 — Test 1: token prop appears in /edit URL
  it("includes the token in the iframe /edit URL", () => {
    const { container } = render(
      <OnlyOfficeEditor
        documentId="doc-abc"
        gatewayUrl="https://gateway.example.com"
        token="jwt-token-123"
      />
    );
    const iframe = container.querySelector("iframe");
    expect(iframe!.src).toBe("https://gateway.example.com/edit?document_id=doc-abc&token=jwt-token-123");
  });

  // Phase 1 — Test 2: no mode param when mode=edit (default)
  it("does not include mode=edit in the iframe URL by default", () => {
    const { container } = render(
      <OnlyOfficeEditor
        documentId="doc-abc"
        gatewayUrl="https://gateway.example.com"
        mode="edit"
      />
    );
    const iframe = container.querySelector("iframe");
    expect(iframe!.src).toBe("https://gateway.example.com/edit?document_id=doc-abc");
  });

  // Phase 1 — Test 3: mode=view appends &mode=view
  it("appends mode=view to the URL when mode is 'view'", () => {
    const { container } = render(
      <OnlyOfficeEditor
        documentId="doc-abc"
        gatewayUrl="https://gateway.example.com"
        mode="view"
      />
    );
    const iframe = container.querySelector("iframe");
    expect(iframe!.src).toBe("https://gateway.example.com/edit?document_id=doc-abc&mode=view");
  });


  // Phase 1 — Test 4: iframe remounts when token or mode changes
  it("remounts the iframe when token or mode changes", () => {
    const { container, rerender } = render(
      <OnlyOfficeEditor
        documentId="doc-1"
        gatewayUrl="https://gateway.example.com"
        token="t1"
        mode="edit"
      />
    );
    const iframe1 = container.querySelector("iframe")!;

    rerender(
      <OnlyOfficeEditor
        documentId="doc-1"
        gatewayUrl="https://gateway.example.com"
        token="t2"
        mode="view"
      />
    );
    const iframe2 = container.querySelector("iframe")!;

    // Without key, React reuses the same DOM element (no remount)
    // With key, React unmounts old + mounts new → different element
    expect(iframe2).not.toBe(iframe1);
    expect(iframe2.src).toBe("https://gateway.example.com/edit?document_id=doc-1&token=t2&mode=view");
  });
});

import { VERSION as SDK_VERSION } from "./OnlyOfficeEditor";

it("exports a version string", () => {
  expect(SDK_VERSION).toBeTruthy();
  expect(typeof SDK_VERSION).toBe("string");
  expect(SDK_VERSION).toMatch(/^\d+\.\d+\.\d+$/);
});
