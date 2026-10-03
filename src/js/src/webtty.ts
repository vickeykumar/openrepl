import * as Cookies from "./cookie";

export const protocols = ["webtty"];
export const IdeLangKey = "IdeLang";
export const IdeContentKey = "IdeContent";
export const IdeFileNameKey = "IdeFileName";
export const CompilerOptionKey = "CompilerOption";
export const CompilerFlagsKey = "CompilerFlags";
export const EnvFlagsKey = "EnvFlags";

export const msgInputUnknown = '0';
export const msgInput = '1';
export const msgPing = '2';
export const msgResizeTerminal = '3';

export const msgUnknownOutput = '0';
export const msgOutput = '1';
export const msgPong = '2';
export const msgSetWindowTitle = '3';
export const msgSetPreferences = '4';
export const msgSetReconnect = '5';
export const msgEvent = '6';

export var sessionCookieObj = new Cookies.SessionCookie("Session");

const clickhandler = () => {
                                const optionMenu = document.getElementById("optionMenu");
                                if(optionMenu!==null) {
                                    const social_count = optionMenu.getElementsByClassName("social-count")[0] as HTMLSpanElement;
                                    social_count.textContent = (parseInt("0"+social_count.textContent)+1).toString();
                                }
                            };

export type eventhandlertype = (eventdata: Object) => void;

export var eventHandler : eventhandlertype = (eventdata: Object) => {
                        console.log("Event data recieved: ", eventdata);

                        }

export function setEventHandler(callback: eventhandlertype): void {
  eventHandler = callback;
}

export const jidHandler = (jid: string) => {
                    console.log("jid recieved: ", jid);
                    const optionMenu = document.getElementById("optionMenu");
                    if(optionMenu!==null) {
                        const option = optionMenu.getElementsByClassName("forkbtn")[0] as HTMLAnchorElement;
                        const social_count = optionMenu.getElementsByClassName("social-count")[0] as HTMLSpanElement;
                        if(option!==null && social_count!==null) {
                            const url = new URL(window.location.href);
                            if(url.searchParams.get('jid')!==null || jid==="") {
                                option.removeAttribute("href"); // disable fork as itself a child/frontend
                                social_count.textContent="0";
                                option.removeEventListener("click", clickhandler);
                                return;
                            }
                            let search = url.search=="" ? "?jid="+jid : url.search+"&jid="+jid;
                            option.setAttribute("href",url.origin+url.pathname+search);
                            option.addEventListener("click", clickhandler);
                        }
                    }
                };

export interface Terminal {
    getID() : string;
    info(): { columns: number, rows: number };
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    removeMessage(): void;
    setWindowTitle(title: string): void;
    setTabTitle(title: string): void;
    setPreferences(value: object): void;
    onInput(callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
    reset(): void;
    hardreset(): void;
    addEventListener(event: string, callback: (e?: any) => void): void;
    removeEventListener(event: string, callback: (e?: any) => void): void;
    dispatchEvent(eventobj: any): void;
    deactivate(): void;
    close(): void;
}

export interface Connection {
    open(): void;
    close(): void;
    send(data: string): void;
    isOpen(): boolean;
    isClosed(): boolean;
    onOpen(callback: () => void): void;
    onReceive(callback: (data: string) => void): void;
    onClose(callback: (closeEvent: object) => void): void;
}

export interface ConnectionFactory {
    create(): Connection;
}

export type CloserArgs = {
    keepdb: boolean;
    keepdbcallbacks: boolean;
};

export interface Icallback {
    (args: CloserArgs): void;
}

export interface WebTTYFactory {
    open(): Icallback;
    dboutput(type: string, data: any): void;
}

const url = new URL(window.location.href);

export class WebTTY {
    term: Terminal;
    connectionFactory: ConnectionFactory;
    args: string;
    authToken: string;
    reconnect: number;
    firebaseref: any;
    Payload: Object;
    iscompiled: boolean;
    private static jid: string = url.searchParams.get('jid')!==null ? ": jid-"+url.searchParams.get('jid') : "";
    // job id of main process

    private static setjid(jid: string): void {
        this.jid = jid;
    }

    private static getjid(): string {
        return this.jid;
    }

    constructor(term: Terminal, connectionFactory: ConnectionFactory, ft: WebTTYFactory, payload:Object, args: string, authToken: string) {
        this.term = term;
        this.connectionFactory = connectionFactory;
        this.args = args;
        this.authToken = authToken;
        this.reconnect = -1;
        this.firebaseref = ft;
        this.Payload = payload;
        this.iscompiled = false;
        if(payload[IdeLangKey] && payload[IdeContentKey]) {
            this.iscompiled = true; //its a compilation request
        }
        if (!this.firebaseref.isprimary && WebTTY.getjid()!=="" && this.args.match("jid=")==null) {
            // add jid only when it is not found, only to secondary terminal tabs
            const jidstr = "jid="+WebTTY.getjid();
            if (this.args==="") {
                this.args+='?'+jidstr;
            } else {
                this.args+='&'+jidstr;
            }
        }
    };

    dboutput(type: string, data: any) {
        if (this.firebaseref.isactive()) {
            this.firebaseref.dboutput(type, data);
        }
    };

    open() {
        const TermOutput = (data: string) => {
            this.term.output(data);
            this.dboutput("output",data);
        };
        // Tells the page about the connection (T6): "connecting", "connected"
        // or "closed" with a kind: exited, killed, timeout, lost, closed or limit.
        const emitState = (state: string, detail: any = {}) => {
            try {
                detail.state = state;
                detail.compiled = this.iscompiled;
                this.term.dispatchEvent(new CustomEvent("ttystate", { detail: detail, bubbles: true }));
            } catch (e) {
                console.log("ttystate event failed: ", e);
            }
        };
        if (!sessionCookieObj.IsSessionCountValid()) {
            TermOutput("Maximum no of connections reached, \
Please close/disconnect the old Terminals to proceed or try after "+sessionCookieObj.expiration+" Minutes.");
            emitState("closed", { kind: "limit" });

            return () => {
                console.log("closing connection in webtty")
            }
        }

        let connection = this.connectionFactory.create();
        let pingTimer: number;
        let reconnectTimeout: number;
        // Set when this page closes the connection itself (Reconnect, language
        // switch, Run, closing a tab). Its close event arrives a moment later,
        // after the new connection has taken over the same terminal, so it
        // must not report a state or schedule a reconnect.
        let closedByPage = false;
        let firecloser = this.firebaseref.open();

        const slaveInputhandler = (e) => {
            //console.log("slaveInputhandler: ", e)
            if ((e) && e.detail.eventT == "input") { 
                connection.send(msgInput + e.detail.Data);
            }
        };

        const setup = () => {
            connection.onOpen(() => {
                emitState("connected");
                const termInfo = this.term.info();

                connection.send(JSON.stringify(
                    {
                        Arguments: this.args,
                        AuthToken: this.authToken,
                        Payload:   this.Payload,
                    }
                ));


                const resizeHandler = (colmuns: number, rows: number) => {
                    connection.send(
                        msgResizeTerminal + JSON.stringify(
                            {
                                columns: colmuns,
                                rows: rows
                            }
                        )
                    );
                };

                this.term.onResize(resizeHandler);
                resizeHandler(termInfo.columns, termInfo.rows);

                this.term.onInput(
                    (input: string) => {
                        connection.send(msgInput + input);
                    }
                );

                pingTimer = setInterval(() => {
                    connection.send(msgPing)
                }, 30 * 1000);

                sessionCookieObj.IncrementSessionCount();
                this.term.addEventListener('slaveinputEvent', slaveInputhandler);
            });

            connection.onReceive((data) => {
                const payload = data.slice(1);
                switch (data[0]) {
                    case msgOutput:
                        TermOutput(atob(payload));
                        break;
                    case msgPong:
                        break;
                    case msgSetWindowTitle:
                        let title = payload;
                        let jid = "";
                        if (payload.trim().indexOf('<') == 0) {
                            try {
                                const parser = new DOMParser();
                                const xmlDoc = parser.parseFromString(payload, "text/xml");
                                if (!xmlDoc.documentElement.getElementsByTagName("parsererror").length) {
                                    const t = xmlDoc.documentElement.getElementsByTagName("title")[0].textContent;
                                    if (t) {
                                        title = t;
                                    }
                                    const j = xmlDoc.documentElement.getElementsByTagName("jid")[0].textContent;
                                    if (j) {
                                        jid = j;
                                    }
                                }
                            } catch(e) {
                                console.log("xml parsererror: ",e);
                            }
                        }
                        
                        if (this.firebaseref.isprimary) {
                            // only primary tab sets jid and window title
                            this.term.setWindowTitle(title+"@OpenREPL");
                            WebTTY.setjid(jid);
                            jidHandler(jid);
                        }
                        // other tabs can update its own tab title
                        this.term.setTabTitle(title);
                        this.dboutput("tab", {
                            op: "title",
                            termid: this.term.getID(),
                            title: title
                        });
                        break;
                    case msgSetPreferences:
                        const preferences = JSON.parse(payload);
                        this.term.setPreferences(preferences);
                        break;
                    case msgSetReconnect:
                        const autoReconnect = JSON.parse(payload);
                        console.log("Enabling reconnect: " + autoReconnect + " seconds");
                        this.reconnect = autoReconnect;
                        break;
                    case msgEvent:
                        if (!this.firebaseref.isprimary) {
                            break; // no need to notify through secondary tabs
                        }
                        const eventdata = JSON.parse(atob(payload));
                        this.dboutput("filebrowser-event", eventdata);  // send filebrowser event to firebase   
                        eventHandler(eventdata);    // fire the event locally
                        break;
                    default:
                        console.log("unsupported data recieved: ",data);
                }
            });

            connection.onClose((closeEvent) => {
                clearInterval(pingTimer);
                this.term.deactivate();
                this.term.showMessage("Connection Closed", 2000);    // tune message timeout accordingly
                if (this.reconnect > 0 && !closedByPage) {
                    reconnectTimeout = setTimeout(() => {
                        connection = this.connectionFactory.create();
                        this.term.reset();
                        setup();
                    }, this.reconnect * 1000);
                }
                sessionCookieObj.DecrementSessionCount();
                console.log("close event: ",closeEvent['code'],closeEvent['reason'], connection.isClosed());
                const closeReason: string = closeEvent['reason'] || "";
                let closeKind = "lost";
                // A gateway whose execution node for this session is away closes the
                // terminal with "execution node is away: retry in 80s": the session is
                // placed again when that time is over. The page counts it down.
                let retryIn = 0;
                // A terminal the site refuses on purpose (maintenance, a language that is
                // switched off, an admin ending the session) is closed with
                // "site notice: <what to tell the visitor>".
                let noticeText = "";
                const away = closeReason.match(/execution node is away: retry in (\d+)s/);
                const notice = closeReason.match(/^site notice: (.*)$/);
                if (away) {
                    closeKind = "away";
                    retryIn = parseInt(away[1], 10);
                } else if (notice) {
                    closeKind = "notice";
                    noticeText = notice[1];
                } else if (closeEvent['code'] == 1000 && closeReason.match("local command")) {
                    closeKind = closeReason.match("killed") ? "killed" : "exited";
                } else if (closeReason.match("failed to create backend")) {
                    closeKind = "failed";
                } else if (closeReason.match(/timeout/i)) {
                    closeKind = "timeout";
                } else if (closeEvent['code'] == 1000 && closeReason.match("client")) {
                    closeKind = "closed";
                }
                if (!closedByPage) {
                    emitState("closed", { kind: closeKind, code: closeEvent['code'], reason: closeReason, retryIn: retryIn, notice: noticeText });
                }
                // The home page shows a banner that explains the stop (scribbler.js
                // showTermBanner), so skip the generic "connection closed" lines there.
                const bannerShown = !closedByPage && closeKind !== "closed" && !!document.getElementById("term-banner");
                switch(closeEvent['code']) {
                    case 1000:
                        if (closeKind == "away") {
                            // The home page shows a banner with a countdown. Elsewhere there
                            // is only the terminal to say so in.
                            if (!bannerShown) {
                                TermOutput("\r\n[Your execution node is away. Try reconnecting in " + retryIn + " sec.]");
                            }
                        } else if (closeKind == "notice") {
                            if (!bannerShown) {
                                TermOutput("\r\n[" + noticeText + "]");
                            }
                        } else if (closeKind == "killed") {
                            TermOutput("\r\n[Program stopped: it was killed, most likely by the memory limit] Jobid: "+WebTTY.getjid());
                        } else if (closeReason.match("local command")) {
                            TermOutput("[Program Exited] Jobid: "+WebTTY.getjid());
                        } else {
                            if (!bannerShown) {
                                TermOutput("connection closed by remote host "+WebTTY.getjid());
                            }
                            if (!this.firebaseref.isprimary && closeEvent['reason'].match("error.*invalid parent id")) {
                                // parent terminal disconnected
                                TermOutput((bannerShown ? "\r\n" : " ") + "[Primary Terminal is disconnected, Please reconnect and try again.]");
                            }
                        }
                        break;

                    case 1005:
                        TermOutput("connection closed.");
                        break;

                    default:
                        if (bannerShown) {
                            break;
                        }
                        TermOutput("connection closed by remote host");
                        if (!this.iscompiled) {
                            let jidstr = WebTTY.getjid();
                            jidstr = (jidstr=="") ? "" : (": "+jidstr);
                            TermOutput("\r\n OR");
                            TermOutput("\r\nResource"+jidstr+" unavailable, Please try again after some time.");
                        }
                }
	    });


            // when tab/window is closed
            window.onbeforeunload = function() {
                sessionCookieObj.DecrementSessionCount();
            };

            emitState("connecting");
            connection.open();
        }

        setup();
        return (args: CloserArgs) => {
            console.log("closing connection in webtty")
	        sessionCookieObj.DecrementSessionCount();
            clearTimeout(reconnectTimeout);
            closedByPage = true;
            connection.close();
            this.term.removeEventListener('slaveinputEvent', slaveInputhandler);
            firecloser(args);
        }
    };
};
