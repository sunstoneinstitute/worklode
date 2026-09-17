// WL-855 spike: mount BlockNote as a React island on the cockpit's document
// page. The island is deliberately the whole of it — no save, no autosave, no
// routing. It answers three questions: does a React root work inside a templ
// page, does a real document body survive the markdown round trip, and does
// any of it run under `script-src 'self'; style-src 'self'` with no nonce.
//
// The page hands the island its input through markup, because the cockpit has
// no inline script to hand it through: #doc-source is a hidden <textarea>
// carrying the stored markdown, #doc-editor is the element the root mounts on.

import { adoptRefusedStyles } from "./cspstyles";
import { createRoot } from "react-dom/client";
import { useCreateBlockNote } from "@blocknote/react";
import { BlockNoteView } from "@blocknote/mantine";
import "@blocknote/mantine/style.css";
import { useEffect } from "react";

declare global {
  interface Window {
    // The round-trip probe the browser check calls: the editor's current
    // document, exported back to markdown.
    lodeEditorMarkdown?: () => Promise<string>;
    // Resolves once the stored markdown has been parsed into blocks, so the
    // check has something to wait on other than a timer.
    lodeEditorReady?: Promise<void>;
  }
}

function Editor({ markdown, onReady }: { markdown: string; onReady: () => void }) {
  const editor = useCreateBlockNote();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const blocks = await editor.tryParseMarkdownToBlocks(markdown);
      if (cancelled) return;
      editor.replaceBlocks(editor.document, blocks);
      window.lodeEditorMarkdown = () => editor.blocksToMarkdownLossy(editor.document);
      onReady();
    })();
    return () => {
      cancelled = true;
    };
  }, [editor, markdown, onReady]);

  return <BlockNoteView editor={editor} />;
}

adoptRefusedStyles();

const mount = document.getElementById("doc-editor");
const source = document.getElementById("doc-source") as HTMLTextAreaElement | null;
if (mount && source) {
  let ready!: () => void;
  window.lodeEditorReady = new Promise<void>((resolve) => {
    ready = resolve;
  });
  createRoot(mount).render(<Editor markdown={source.value} onReady={ready} />);
}
