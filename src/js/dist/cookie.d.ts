export declare const maxconnections = 1;
export declare class SessionCookie {
    name: string;
    expiration: number;
    isSet: boolean;
    constructor(cookieName: string);
    encode(str: any): string;
    decode(str: any): string;
    IncrementSessionCount(): void;
    DecrementSessionCount(): void;
    IsSessionCountValid(): boolean;
}
