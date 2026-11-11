import * as Cookies from 'es-cookie';

export const maxconnections = 3;	// maximum connections allowed per browser

// A connection that has not refreshed its entry for this long is taken to be gone (the tab was closed
// without a chance to say so). Open connections refresh theirs with every ping, every 30 seconds.
export const staleAfterMs = 2 * 60 * 1000;

// SessionCookie counts the terminals this browser has open, across tabs, in one cookie. The cookie holds one
// entry per open connection (its id and when it last said it was alive), not a number: a number that is
// counted up and down goes wrong as soon as one decrease is missed (a tab closed with several terminals, an
// unload event the browser does not run, two tabs writing at the same moment) and then locks the user out
// until the cookie expires. With entries, each connection takes away only its own, and one that never does
// stops counting after staleAfterMs.
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

	// entries returns the connections that count: their id and when they last said they were alive. A cookie
	// in another format (the number of the earlier version, or anything unreadable) counts nothing.
	entries(now: number = Date.now()): { [id: string]: number } {
		let out: { [id: string]: number } = {};
		try {
			const raw = Cookies.get(this.name);
			if (raw === undefined) {
				return out;
			}
			const parsed = JSON.parse(atob(raw));
			if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
				return out;
			}
			for (const id of Object.keys(parsed)) {
				const seen = Number(parsed[id]);
				if (isFinite(seen) && now - seen < staleAfterMs) {
					out[id] = seen;
				}
			}
		} catch (e) {
			// not ours: nothing counts
		}
		return out;
	}

	private save(entries: { [id: string]: number }): void {
		if (Object.keys(entries).length === 0) {
			Cookies.remove(this.name);
			return;
		}
		let minutes = new Date();
		minutes.setMinutes(minutes.getMinutes() + this.expiration);
		Cookies.set(this.name, this.encode(JSON.stringify(entries)), { expires: minutes });
	}

	// Open records a connection that has just opened.
	Open(id: string): void {
		if (!id) {
			return;
		}
		let entries = this.entries();
		entries[id] = Date.now();
		this.save(entries);
	};

	// Touch says that a connection is still alive.
	Touch(id: string): void {
		if (!id) {
			return;
		}
		let entries = this.entries();
		if (entries[id] === undefined) {
			return; // it was dropped (it counted as gone, or was closed): it is not brought back by a ping
		}
		entries[id] = Date.now();
		this.save(entries);
	};

	// Close takes a connection away, once: a second call for the same id changes nothing.
	Close(id: string): void {
		if (!id) {
			return;
		}
		let entries = this.entries();
		if (entries[id] === undefined) {
			return;
		}
		delete entries[id];
		this.save(entries);
	};

	// Count is how many connections this browser has open.
	Count(): number {
		return Object.keys(this.entries()).length;
	}

	IsSessionCountValid(): boolean {
		return this.Count() < maxconnections;
	}
};
