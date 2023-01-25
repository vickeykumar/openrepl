import * as firebase from 'firebase';
import { Terminal } from "./webtty";


var dbpath = "";
export const getExampleRef = () => {
      if(window['dbpath']) {
        return window['dbpath'];
      }
      var ref = firebase.database().ref();
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
    apiKey: "AIzaSyCcHshsJr7GXkyIaFOFRgfu76mS9eEUHOA",
    authDomain: "root-grammar-251415.firebaseapp.com",
    databaseURL: "https://root-grammar-251415.firebaseio.com"
};

export const InitializeApp = () => {
	firebase.initializeApp(firebaseconfig);
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

    constructor(term: Terminal, master: boolean) {
        this.term = term;
        this.master = master;
        this.active = false;
        //one time generation of dbpath
        if (dbpath == "") {
        	dbpath = getExampleRef();
            window['dbpath']=dbpath;
        }
        this.dbpath = dbpath;
    };

    open() {
    	this.firebasedbref = firebase.database().ref(this.dbpath);
    	this.active = true;
        const optionhandler = () => {
            const optionMenu = document.getElementById("optionMenu");
            if(optionMenu!==null) {
                const option = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement).value;
                this.firebasedbref.push ({
                   eventT: "option",
                   Data: option
                });
                //console.log("optionhandler: "+option);
            }
        };

        const optionrunhandler = (e) => {
            if ((e) && (e.detail) && e.detail.optionT==="optiondebug") {
                this.dboutput("optiondebug", "optiondebug");
            } else {
                this.dboutput("optionrun", "optionrun");
            }
        };

    	const setupMaster = () => {
            let masterterm = this.term;
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
                    default:
                        console.log("unhandled type: ", d.eventT, d.Data);
                        break;
                }
			});
        };
        const setupSlave = () => {
            let slaveterm = this.term;
        	this.firebasedbref.on("child_added", function(data, prevChildKey) {
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
                        console.log("slave caught optionevent:"+d.Data)
                        slaveterm.hardreset();
                        const optionMenu = document.getElementById("optionMenu");
                        if(optionMenu!==null) {
                            const SelectOption = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement);
                            if (SelectOption !== null && SelectOption.value !== d.Data) {   //once
                                SelectOption.value = d.Data;
                                var event = new Event('change');
                                SelectOption.dispatchEvent(event);
                            }
                        }
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

    	return () => {
            console.log("closing connection in Firebase");
            this.active = false;
            this.firebasedbref.off(); // detach all callback
            this.term.removeEventListener("optionrun", optionrunhandler);
            this.term.reset();
            this.term.deactivate()
            if (this.master) {
                this.firebasedbref.remove();    // remove database upon closure of master
            }
        }
    };

    dboutput(type: string, data: string) {
    	if (this.active) {		// only master can generate output event
    		//console.log("db output event: ", type, data);
	    	this.firebasedbref.push ({
			   eventT: type,
			   Data: data
			});
    	}
    };
};
