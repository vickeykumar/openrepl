import * as Cookies from "./cookie";

export const protocols = ["webtty"];

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

export var sessionCookieObj = new Cookies.SessionCookie("Session");

const clickhandler = () => {
                                const optionMenu = document.getElementById("optionMenu");
                                if(optionMenu!==null) {
                                    const social_count = optionMenu.getElementsByClassName("social-count")[0] as HTMLSpanElement;
                                    social_count.textContent = (parseInt("0"+social_count.textContent)+1).toString();
                                }
                            };

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
    info(): { columns: number, rows: number };
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    removeMessage(): void;
    setWindowTitle(title: string): void;
    setPreferences(value: object): void;
    onInput(callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
    reset(): void;
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


export class WebTTY {
    term: Terminal;
    connectionFactory: ConnectionFactory;
    args: string;
    authToken: string;
    reconnect: number;

    constructor(term: Terminal, connectionFactory: ConnectionFactory, args: string, authToken: string) {
        this.term = term;
        this.connectionFactory = connectionFactory;
        this.args = args;
        this.authToken = authToken;
        this.reconnect = -1;
    };

    open() {
        if (!sessionCookieObj.IsSessionCountValid()) {
            this.term.output("Maximum no of connections reached, \
Please close/disconnect the old Terminals to proceed or try after "+sessionCookieObj.expiration+" Minutes.");

            return () => {
                console.log("closing connection in webtty")
            }
        }

        let connection = this.connectionFactory.create();
        let pingTimer: number;
        let reconnectTimeout: number;

        const setup = () => {
            connection.onOpen(() => {
                const termInfo = this.term.info();

                connection.send(JSON.stringify(
                    {
                        Arguments: this.args,
                        AuthToken: this.authToken,
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
            });

            connection.onReceive((data) => {
                const payload = data.slice(1);
                switch (data[0]) {
                    case msgOutput:
                        this.term.output(atob(payload));
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
                        this.term.setWindowTitle(title);
                        jidHandler(jid);
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
                    default:
                        console.log("unsupported data recieved: ",data);
                }
            });

            connection.onClose((closeEvent) => {
                clearInterval(pingTimer);
                this.term.deactivate();
                this.term.showMessage("Connection Closed", 2000);    // tune message timeout accordingly
                if (this.reconnect > 0) {
                    reconnectTimeout = setTimeout(() => {
                        connection = this.connectionFactory.create();
                        this.term.reset();
                        setup();
                    }, this.reconnect * 1000);
                }
                sessionCookieObj.DecrementSessionCount();
                console.log("close event: ",closeEvent['code'],closeEvent['reason'], connection.isClosed());
                const url = new URL(window.location.href);
                let jidstr = url.searchParams.get('jid')!==null ? ": jid-"+url.searchParams.get('jid') : "";
                switch(closeEvent['code']) {
                    case 1000:
                        this.term.output("connection closed by remote host"+jidstr);
                        break;

                    case 1005:
                        this.term.output("connection closed.");
                        break;

                    default:
                        this.term.output("connection closed by remote host");
                        this.term.output("\r\n OR");
                        this.term.output("\r\nResource"+jidstr+" unavailable, Please try again after some time.");
                }
	    });


            // when tab/window is closed
            window.onbeforeunload = function() {
                sessionCookieObj.DecrementSessionCount();
            };

            connection.open();
        }

        setup();
        return () => {
            console.log("closing connection in webtty")
	        sessionCookieObj.DecrementSessionCount();
            clearTimeout(reconnectTimeout);
            connection.close();
        }
    };
};
