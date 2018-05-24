import * as Cookies from 'es-cookie';

export const maxconnections = 1;	// maximum connections allowed per browser

export class SessionCookie {
	name: string;
	expiration: number;	// Minutes

	constructor(cookieName: string) {
		this.name = cookieName;
		this.expiration = 15;
	};

	// base64 encoder
	encode(str): string {
		return btoa(str);
	};

	// base64 decoder
	decode(str): string {
		if (str === undefined) {
			return "0";
		}
		return atob(str);
	}

	IncrementSessionCount(): void {
		var val = Cookies.get(this.name);
		var value = Number(this.decode(val));
		if (isNaN(value)) {
			console.log("cookie Not found ");
			value = 0;
		}
		value++;
		let minutes = new Date();
		minutes.setMinutes(minutes.getMinutes() + this.expiration);
		Cookies.set(this.name, this.encode(value.toString()), { expires: minutes });
	};

	DecrementSessionCount(): void {
		var val = Cookies.get(this.name);
		var value = Number(this.decode(val));
		if (isNaN(value)) {
			console.log("cookie Not found ");
			return
		}
		if (value > 0) {
			value--;
			let minutes = new Date();
			minutes.setMinutes(minutes.getMinutes() + this.expiration);
			Cookies.set(this.name, this.encode(value.toString()), { expires: minutes });
		}
	};

	IsSessionCountValid(): boolean {
		var val = Cookies.get(this.name);
		var value = Number(this.decode(val));
		if (isNaN(value)) {
			console.log("cookie Not found ");
			value = 0;
		}

		if (value >= maxconnections) {
			return false;
		}
		return true
	}
};