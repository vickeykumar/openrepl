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
                        this.term.setWindowTitle(payload);
                        break;
                    case msgSetPreferences:
                        const preferences = JSON.parse(payload);
                        this.term.setPreferences(preferences);
                        break;
                    case msgSetReconnect:
                        const autoReconnect = JSON.parse(payload);
                        console.log("Enabling reconnect: " + autoReconnect + " seconds")
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
                console.log("close event: ",closeEvent['code'],closeEvent['reason']);
                switch(closeEvent['code']) {
                    case 1000:
                        this.term.output("connection closed by remote host.");
                        break;

                    case 1005:
                        this.term.output("connection closed.");
                        break;

                    default:
                        this.term.output("connection closed.");
                        this.term.output("\r\nResource temporarily unavailable, Please try again after some time.");
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
