import { Terminal } from "./webtty";
export declare const getExampleRef: () => any;
export declare const firebaseconfig: {
    apiKey: string;
    authDomain: string;
    projectId: string;
    databaseURL: string;
};
export declare const InitializeApp: () => void;
export declare const DisableShareBtn: (name: string) => void;
export declare class FireTTY {
    term: Terminal;
    master: boolean;
    active: boolean;
    firebasedbref: any;
    dbpath: string;
    constructor(term: Terminal, master: boolean);
    open(): () => void;
    dboutput(type: string, data: string): void;
}
