import { Terminal as XTerminal, IDisposable } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";

// xterm.js 6 (T19). Font, size and colours are terminal options now; the
// renderer measures the font itself instead of taking it from the CSS.
const CODE_FONT = "'JetBrains Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace";

const THEME = {
    background: "#09090D",
    foreground: "#E6E4EC",
    cursor: "#F2F0EC",
    cursorAccent: "#09090D",
    selectionBackground: "rgba(254, 106, 107, 0.35)",
    selectionForeground: "#FFFFFF",
    // the ANSI colours, tuned to read well on the near-black background
    black: "#2E2E3A",
    red: "#FF7B7C",
    green: "#7FD6A4",
    yellow: "#F2C66D",
    blue: "#7AA7FF",
    magenta: "#D39BFF",
    cyan: "#6FD3E3",
    white: "#C9C7D1",
    brightBlack: "#6C6A76",
    brightRed: "#FF9C9D",
    brightGreen: "#A3E8BF",
    brightYellow: "#F7D98F",
    brightBlue: "#A3C2FF",
    brightMagenta: "#E3BDFF",
    brightCyan: "#9BE3EE",
    brightWhite: "#F2F0EC",
};

export class Xterm {
    elem: HTMLElement;
    term: XTerminal;
    fitAddon: FitAddon;
    resizeListener: () => void;
    decoder: ByteStringDecoder;

    message: HTMLElement;
    messageTimeout: number;
    messageTimer: number;

    private listeners: IDisposable[] = [];
    private inputCallbacks: ((input: string) => void)[] = [];
    private sizeObserver: ResizeObserver | null = null;
    private fitFrame = 0;
    private lastSize = "";

    constructor(elem: HTMLElement) {
        this.elem = elem;
        this.term = new XTerminal({
            fontFamily: CODE_FONT,
            fontSize: 14,
            lineHeight: 1.0,
            cursorBlink: true,
            scrollback: 5000,
            theme: THEME,
            macOptionIsMeta: false,
            // links open in a new tab, without access back to this page
            linkHandler: {
                activate: (event: MouseEvent, uri: string) => { openLink(uri); },
                allowNonHttpProtocols: false,
            },
        });
        this.fitAddon = new FitAddon();
        this.term.loadAddon(this.fitAddon);
        // plain URLs in the output become clickable
        this.term.loadAddon(new WebLinksAddon((event: MouseEvent, uri: string) => { openLink(uri); }));

        this.message = elem.ownerDocument.createElement("div");
        this.message.className = "xterm-overlay";
        this.messageTimeout = 2000;

        // Fit to the element whenever its size changes: window resizes, the
        // editor split, the Files panel, a tab becoming visible again.
        this.resizeListener = () => { this.scheduleFit(); };
        window.addEventListener("resize", this.resizeListener);

        this.term.open(elem);
        // a name for screen readers (T15)
        const input = this.term.textarea;
        if (input) input.setAttribute("aria-label", "Terminal input");
        // Focusing on phones would scroll the page and open the keyboard (T10).
        if (window.innerWidth > 800) this.term.focus();

        // The terminal measures the code font when it opens. If the web font
        // arrives later, measure again so columns line up (T2).
        const fonts = (document as any).fonts;
        if (fonts && fonts.load) {
            fonts.load("14px 'JetBrains Mono'").then(() => {
                this.term.options.fontFamily = "monospace";
                this.term.options.fontFamily = CODE_FONT;
                this.lastSize = "";
                this.fit(false);
            }).catch(() => {});
        }

        if (typeof ResizeObserver !== "undefined") {
            this.sizeObserver = new ResizeObserver(() => { this.scheduleFit(); });
            this.sizeObserver.observe(elem);
        }
        this.fit(false);

        this.decoder = new ByteStringDecoder();
    };

    private scheduleFit() {
        if (this.fitFrame) return;
        this.fitFrame = requestAnimationFrame(() => {
            this.fitFrame = 0;
            this.fit(true);
        });
    }

    // Resizes the grid to the element; a hidden terminal is left as it is.
    private fit(announce: boolean) {
        if (!this.elem.offsetParent) return;
        const dims = this.fitAddon.proposeDimensions();
        if (!dims || !isFinite(dims.cols) || !isFinite(dims.rows) || dims.cols < 2 || dims.rows < 1) return;
        const size = dims.cols + "x" + dims.rows;
        if (size === this.lastSize) return;
        const first = this.lastSize === "";
        this.lastSize = size;
        this.fitAddon.fit();
        if (announce && !first) {
            this.showMessage(String(this.term.cols) + "x" + String(this.term.rows), this.messageTimeout);
        }
    }

    getID() : string {
        // return id of the terminal
        if (this.elem.id) {
            return this.elem.id;
        }
        return "terminal";
    }

    info(): { columns: number, rows: number } {
        return { columns: this.term.cols, rows: this.term.rows };
    };

    output(data: string) {
        this.term.write(this.decoder.decode(data));
    };

    // Sends keys to the REPL as if typed (the extra-keys row on phones).
    typeInput(data: string) {
        this.inputCallbacks.forEach((callback) => callback(data));
    }

    // The last `lines` lines of the screen and scrollback, as plain text.
    recentText(lines: number): string {
        const buffer = this.term.buffer.active;
        const out: string[] = [];
        // up to the cursor's line; the rows below it are still empty
        const end = Math.min(buffer.length, buffer.baseY + buffer.cursorY + 1);
        for (let i = Math.max(0, end - lines); i < end; i++) {
            const line = buffer.getLine(i);
            if (!line) continue;
            if (line.isWrapped && out.length) out[out.length - 1] += line.translateToString(true);
            else out.push(line.translateToString(true));
        }
        return out.join("\n");
    }

    focus() {
        this.term.focus();
    }

    showMessage(message: string, timeout: number) {
        this.message.textContent = message;
        this.elem.appendChild(this.message);

        if (this.messageTimer) {
            clearTimeout(this.messageTimer);
        }
        if (timeout > 0) {
            this.messageTimer = window.setTimeout(() => {
                this.removeMessage();
            }, timeout);
        }
    };

    removeMessage(): void {
        if (this.message.parentNode == this.elem) {
            this.elem.removeChild(this.message);
        }
    }

    setWindowTitle(title: string) {
        document.title = title;
    };

    setTabTitle(title: string) {
        const elem = this.elem as HTMLElement & { tab: any };
        if (elem.tab) {
            const tab = elem.tab as HTMLDivElement;
            if (tab) {
                const titleSpan = tab.querySelector('.tab-title') as HTMLElement;
                if (titleSpan) {
                    titleSpan.textContent = title;
                }
            }
        }
    };

    setPreferences(value: object) {
    };

    onInput(callback: (input: string) => void) {
        this.inputCallbacks.push(callback);
        this.listeners.push(this.term.onData((data) => {
            callback(data);
        }));
    };

    onResize(callback: (colmuns: number, rows: number) => void) {
        this.listeners.push(this.term.onResize((size) => {
            callback(size.cols, size.rows);
        }));
    };

    deactivate(): void {
        this.listeners.forEach((l) => l.dispose());
        this.listeners = [];
        this.inputCallbacks = [];
        this.term.blur();
    }

    reset(): void {
        this.removeMessage();
        this.term.clear();
    }

    hardreset(): void {
        this.term.reset();
    }

    addEventListener(event: string, callback: (e?: any) => void) {
        this.elem.addEventListener(event, callback);
    };

    removeEventListener(event: string, callback: (e?: any) => void) {
        this.elem.removeEventListener(event, callback);
    };

    dispatchEvent(eventobj: any) {
        this.elem.dispatchEvent(eventobj);
    };

    close(): void {
        console.log("closing connection for window xterm")
        window.removeEventListener("resize", this.resizeListener);
        if (this.sizeObserver) this.sizeObserver.disconnect();
        if (this.fitFrame) cancelAnimationFrame(this.fitFrame);
        this.listeners.forEach((l) => l.dispose());
        this.listeners = [];
        this.term.dispose();
    }
}

function openLink(uri: string) {
    const w = window.open(uri, "_blank", "noopener");
    if (w) w.opener = null;
}

// Output arrives as a string of bytes (one character per byte, from atob).
// Decodes it as UTF-8, keeping a character split across two messages.
class ByteStringDecoder {
    private decoder = new TextDecoder("utf-8");

    decode(bytes: string): string {
        const buf = new Uint8Array(bytes.length);
        for (let i = 0; i < bytes.length; i++) buf[i] = bytes.charCodeAt(i) & 0xff;
        return this.decoder.decode(buf, { stream: true });
    }
}
