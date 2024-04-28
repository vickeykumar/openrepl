import * as firebase from 'firebase';
import { Terminal, eventHandler, eventhandlertype, CloserArgs} from "./webtty";


var dbpath = "";
var getrepl_firebasedbref = () => {
	return firebase.database().ref("openrepl");
};

var tabeventHandler : eventhandlertype = (eventdata: Object) => {
                        console.log("Tab Event data recieved: ", eventdata);

                        }

export function setTabEventHandler(callback: eventhandlertype): void {
  tabeventHandler = callback;
}

export const UID = Math.random().toString();
const MIN_SAFE_INTEGER = 1<<31;

export const getExampleRef = () => {
      if(window['dbpath']) {
        return window['dbpath'];
      }
      var ref = getrepl_firebasedbref();
      var hash = window.location.hash.replace(/#/g, '');
      if (hash) {
        window['dbpath'] = hash;
        return hash;
      }
      ref = ref.push();
      if (ref.key) {
      	return ref.key;
      }
      return "xyz";
};

export const firebaseconfig = {
    apiKey: "AIzaSyASgAaRv6yXUJQVcHaA_lRFVMy9AYZeRls",
    authDomain: "openrepl-app.firebaseapp.com",
    projectId: "openrepl-app",
    databaseURL: "https://openrepl-app-default-rtdb.firebaseio.com"
};

export const InitializeApp = () => {
    if (firebase.apps.length === 0) {
	   firebase.initializeApp(firebaseconfig);
    }
};

export const DisableShareBtn = (name: string) => {
    const share_it = document.getElementById("share-it") as HTMLInputElement;
    if (share_it) share_it.value = "Not Supported for : "+name;
};

export class FireTTY {
    term: Terminal;
    master: boolean;
    active: boolean;
    firebasedbref: any;
    dbpath: string;
    lastrestarted: string;
    uid: string;
    isprimary: boolean; 
    // if this is primary instance(tab) that will communicate for all major roles

    // static property to share accross class
    private static sharedlastrestarted: string = MIN_SAFE_INTEGER.toString();
    private static applyingchanges: boolean = false;

    private static setngetrestart(): string {
        // updates the shared restart timing of all the firetty terminals in this window.
        this.sharedlastrestarted = Date.now().toString();
        return this.sharedlastrestarted;
    }

    private static getrestart(): string {
        return this.sharedlastrestarted;
    }

    isactive(): boolean {
        return this.active;
    }

    constructor(term: Terminal, master: boolean, isprimary: boolean) {
        this.term = term;
        this.master = master;
        this.isprimary = isprimary;
        this.active = false;
        //one time generation of dbpath
        if (dbpath == "") {
        	dbpath = getExampleRef();
            window['dbpath']=dbpath;
        }
        this.dbpath = dbpath;
        this.lastrestarted = FireTTY.getrestart();
        this.uid = UID;
    };

    open() {
        // work with terminals id
    	this.firebasedbref = getrepl_firebasedbref().child(this.dbpath).child(this.term.getID());
    	this.active = true;
        const optionhandler = () => {
            const optionMenu = document.getElementById("optionMenu");
            if(optionMenu!==null) {
                const option = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement).value;
                if (!this.master && this.lastrestarted==MIN_SAFE_INTEGER.toString()) {
                    // slave don't need to update the option in firebase at the start
                    this.lastrestarted = FireTTY.setngetrestart();
                    return;
                }
                this.lastrestarted = FireTTY.setngetrestart();
                if (!this.master && FireTTY.applyingchanges) {
                    // no notification back as i am applying my notification change here.
                    console.log("slave applying its own change request...")
                    FireTTY.applyingchanges = false;
                    return;
                }
                this.firebasedbref.push ({
                   eventT: "option",
                   Data: option,
                   last: this.lastrestarted,
                   uid: this.uid
                });
                console.log("optionhandler updated by uid: "+this.uid + " option: " + option + " at " + this.lastrestarted);
            }
        };

        const optionrunhandler = (e) => {
            if (!this.master && FireTTY.applyingchanges) {
                    // no notification back as i am applying my notification change here.
                    console.log("slave applying its own run request...")
                    FireTTY.applyingchanges = false;
                    return;
            }

            if ((e) && (e.detail) && e.detail.optionT==="optiondebug") {
                this.dboutput("optiondebug", "optiondebug");
            } else {
                this.dboutput("optionrun", "optionrun");
            }
        };

    	const setupMaster = () => {
            let masterterm = this.term;
            let thisinst = this;
    		this.firebasedbref.on("child_added", function(data, prevChildKey) {
				let d = data.val();
                //console.log("master: ", d.eventT, d.Data);
                switch (d.eventT) {
                    case "input":
                        //master will only recieve input from other slaves and export dboutput
                        const event = new CustomEvent('slaveinputEvent', {
                          detail: { eventT: d.eventT, Data: d.Data }
                        });
                        masterterm.dispatchEvent(event)
                        break;
                    case "output":
                        //do nothing
                        break;
                    case "optionrun":
                        console.log("optionrun requested at master.");
                        masterterm.dispatchEvent(new Event("optionrun"));
                        break;
                    case "optiondebug":
                        console.log("optiondebug requested at master.");
                        masterterm.dispatchEvent(new Event("optiondebug"));
                        break;
                    case "option":
                        console.log("master caught optionevent:"+d.Data+" event: ", d);
                        if (d.uid==thisinst.uid) {
                            console.log("event triggered by me only, skipping..");
                            return;
                        }
                        if (d.last<=thisinst.lastrestarted) {
                            // = check as i am master
                            console.log("ignoring previous restarts.");
                            return;
                        }
                        const optionMenu = document.getElementById("optionMenu");
                        if(optionMenu!==null) {
                            const SelectOption = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement);
                            if (SelectOption !== null) {   //once
                                SelectOption.value = d.Data;
                                let event = new Event('change');
                                SelectOption.dispatchEvent(event);
                            }
                        }
                        break;
                    case "tab":
                        console.log("master recieved tabevent: ", d);
                        if (d.uid==thisinst.uid) {
                            console.log(d.Data.op+"tab event triggered by me only, skipping..");
                            return;
                        }
                        tabeventHandler(d.Data);
                        break;
                    default:
                        console.log("unhandled type: ", d.eventT, d.Data);
                        break;
                }
			});
        };
        const setupSlave = () => {
            let slaveterm = this.term;
        	this.firebasedbref.on("child_added", (data, prevChildKey) => {
                let d = data.val();
                //console.log("slave: ", d.eventT, d.Data);
                switch (d.eventT) {
                    case "input":
                        //do nothing
                        break;
                    case "output":
                        slaveterm.output(d.Data);
                        break;
                    case "option":
                        console.log("slave caught optionevent:"+d.Data+" event: ", d);
                        if (d.uid==this.uid) {
                            console.log("event triggered by me only, skipping..");
                            return;
                        }
                        if (d.last<this.lastrestarted) {
                            console.log("ignoring previous restarts.");
                            return;
                        }
                        slaveterm.hardreset();
                        const optionMenu = document.getElementById("optionMenu");
                        if(optionMenu!==null) {
                            const SelectOption = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement);
                            // slave will apply the changes even if it is not active (reactivate).
                            // if it is already active any changes on the other side will automatically come to me.
                            if (SelectOption !== null) {
                                if (SelectOption.value !== d.Data) {
                                    SelectOption.value = d.Data;
                                    let event = new Event('change');
                                    FireTTY.applyingchanges = true; 
                                    SelectOption.dispatchEvent(event);
                                    // fire the event back to restart the terminal with new changeset
                                } else if (!this.active) {
                                    // someone is trying to reach me but i am not active, reactivate..
                                    // probably master trying to run.. activate by running firetty
                                    SelectOption.value = d.Data;
                                    FireTTY.applyingchanges = true; 
                                    this.term.dispatchEvent(new Event("optionrun"));
                                }
                            } 
                        }
                        break;
                    case "filebrowser-event":
                        console.log("filebrowser-event type: ", d.Data);
                        eventHandler(d.Data);
                        break;
                    case "tab":
                        console.log("Slave recieved tabevent: ", d);
                        if (d.uid==this.uid) {
                            console.log(d.Data.op+"tab event triggered by me only, skipping..");
                            return;
                        }
                        tabeventHandler(d.Data);
                        break;
                    default:
                        console.log("unhandled type: ", d.eventT, d.Data);
                        break;
                } 
			});
            this.term.onInput(
                (input: string) => {
                        this.dboutput("input", input);
                }
            );

            this.firebasedbref.child("Master").once("value", snapshot => {  // notify if Master is unavailable
                   if (!snapshot.exists()){
                      slaveterm.output("\r\nMaster Terminal is unavailable");
		      slaveterm.output("\r\nPlease visit: "+window.location.origin+" to start a new session");
		      this.active = false;
                    }
            });
            this.term.addEventListener("optionrun", optionrunhandler);
        };
    	const enablehandler = () => {
                                const optionMenu = document.getElementById("optionMenu");
                                if(optionMenu!==null) {
                                    const share_it = document.getElementById("share-it") as HTMLInputElement;
                                    share_it.value = window.location.origin+window.location.pathname+window.location.search+"#"+this.dbpath;
                                }
                            };

        if (!this.master) {			//Slave
        	enablehandler();
            optionhandler();
        	setupSlave();
        } else {					// Master
            this.firebasedbref.set ({
                Master: {
                    eventT: "Master",
                   Data: ""
                }
            });
            enablehandler();
            optionhandler();        // master controls options
            setupMaster();
        }

    	return (args: CloserArgs) => {
            console.log("closing connection in Firebase");
            this.active = false;
            if (!args.keepdbcallbacks) {
                this.firebasedbref.off(); // detach all callback
            }
            this.term.removeEventListener("optionrun", optionrunhandler);
            this.term.reset();
            this.term.deactivate()
            if (!args.keepdb && this.master) {
                this.firebasedbref.remove();    // remove database upon closure of master
            }
        }
    };

    dboutput(type: string, data: any) {
    		//console.log("db output event: ", type, data);
	    	this.firebasedbref.push ({
			   eventT: type,
			   Data: data,
               uid: this.uid,
			});
    };
};
