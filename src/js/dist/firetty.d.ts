import { Terminal, CloserArgs } from "./webtty";
export declare const UID: string;
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
    lastrestarted: string;
    uid: string;
    private static sharedlastrestarted;
    private static applyingchanges;
    private static setngetrestart();
    private static getrestart();
    constructor(term: Terminal, master: boolean);
    open(): (args: CloserArgs) => void;
    dboutput(type: string, data: any): void;
}
