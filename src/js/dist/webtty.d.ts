import * as Cookies from "./cookie";
export declare const protocols: string[];
export declare const IdeLangKey = "IdeLang";
export declare const IdeContentKey = "IdeContent";
export declare const IdeFileNameKey = "IdeFileName";
export declare const CompilerOptionKey = "CompilerOption";
export declare const CompilerFlagsKey = "CompilerFlags";
export declare const EnvFlagsKey = "EnvFlags";
export declare const msgInputUnknown = "0";
export declare const msgInput = "1";
export declare const msgPing = "2";
export declare const msgResizeTerminal = "3";
export declare const msgUnknownOutput = "0";
export declare const msgOutput = "1";
export declare const msgPong = "2";
export declare const msgSetWindowTitle = "3";
export declare const msgSetPreferences = "4";
export declare const msgSetReconnect = "5";
export declare const msgEvent = "6";
export declare var sessionCookieObj: Cookies.SessionCookie;
export declare type eventhandler = (eventdata: Object) => void;
export declare var eventHandler: eventhandler;
export declare function setEventHandler(callback: eventhandler): void;
export declare const jidHandler: (jid: string) => void;
export interface Terminal {
    info(): {
        columns: number;
        rows: number;
    };
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    removeMessage(): void;
    setWindowTitle(title: string): void;
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
export declare type CloserArgs = {
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
export declare class WebTTY {
    term: Terminal;
    connectionFactory: ConnectionFactory;
    args: string;
    authToken: string;
    reconnect: number;
    firebaseref: any;
    Payload: Object;
    iscompiled: boolean;
    constructor(term: Terminal, connectionFactory: ConnectionFactory, ft: WebTTYFactory, payload: Object, args: string, authToken: string);
    dboutput(type: string, data: any): void;
    open(): (args: CloserArgs) => void;
}
