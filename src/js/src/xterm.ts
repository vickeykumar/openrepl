import * as bare from "xterm";
import { lib } from "libapps"


bare.loadAddon("fit");

export class Xterm {
    elem: HTMLElement;
    term: bare;
    resizeListener: () => void;
    decoder: lib.UTF8Decoder;

    message: HTMLElement;
    messageTimeout: number;
    messageTimer: number;


    constructor(elem: HTMLElement) {
        this.elem = elem;
        this.term = new bare();

        this.message = elem.ownerDocument.createElement("div");
        this.message.className = "xterm-overlay";
        this.messageTimeout = 2000;

        this.resizeListener = () => {
            this.term.fit();
            this.term.scrollToBottom();
            this.showMessage(String(this.term.cols) + "x" + String(this.term.rows), this.messageTimeout);
        };

        this.term.on("open", () => {
            this.resizeListener();
            window.addEventListener("resize", () => { this.resizeListener(); });
        });

        this.term.open(elem, true);

        this.decoder = new lib.UTF8Decoder()
    };

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

    showMessage(message: string, timeout: number) {
        this.message.textContent = message;
        this.elem.appendChild(this.message);

        if (this.messageTimer) {
            clearTimeout(this.messageTimer);
        }
        if (timeout > 0) {
            this.messageTimer = setTimeout(() => {
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
        this.term.on("data", (data) => {
	     callback(data);
        });

    };

    onResize(callback: (colmuns: number, rows: number) => void) {
        this.term.on("resize", (data) => {
            callback(data.cols, data.rows);
        });
    };

    deactivate(): void {
        this.term.off("data");
        this.term.off("resize");
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
        this.term.destroy();
    }
}
