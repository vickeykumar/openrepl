import { Hterm } from "./hterm";
import { Xterm } from "./xterm";
import { Terminal, WebTTY, protocols, jidHandler, Icallback, WebTTYFactory, IdeLangKey, IdeContentKey, IdeFileNameKey, CompilerOptionKey, CompilerFlagsKey, EnvFlagsKey, CloserArgs} from "./webtty";
import { ConnectionFactory } from "./websocket";
import { FireTTY, DisableShareBtn } from "./firetty";

const option2args = {
            "c":"arg=-xc&arg=-noruntime",
          };

// list of languages can't be handled in backend, java because of jvm
const unhandledLanguages: string[] = ['java', 'javascript'];

// Define your custom interface extending HTMLElement
export interface CustomHTMLElement extends HTMLElement {
    gottyterm: any; // save a reference to gottyterm for future reference, if this is tab element.
    tab: any; // save the tab information corresponding to this terminal element.
    isprimary: boolean; // if this corressponds to primary terminal tab element
}

function isMaster() : boolean {
    var hash = window.location.hash.replace(/#/g, '');
    if (!hash) {
        return true;
    } else {
        return false;
    }
}

function handleTerminalOptions(elem, option, event="optionchange") {
    var flag = true;
    if (option!==null && elem!==null) {
        var javaframe = elem.getElementsByClassName("javaframe")[0];
        if (javaframe !== undefined) {
            elem.removeChild(javaframe);
        }
        switch (option) {
                case "java":
                    // code...
                    if (event==="optionrun") {
                        break;
                    }
                    var iframe = document.createElement("IFRAME");
                    iframe.setAttribute("class","javaframe");
                    iframe.setAttribute("src","https://tryjshell.org");
                    iframe.setAttribute("style","width: inherit; height: inherit; border: 0px;");
                    elem.appendChild(iframe);
                    DisableShareBtn(option);
                    flag=false;
                    break;

                case "javascript":
                    // code...
                    var iframe = document.createElement("IFRAME");
                    iframe.setAttribute("class","javaframe");
                    iframe.setAttribute("src","./jsconsole.html");
                    iframe.setAttribute("style","width: inherit; height: inherit; border: 0px;");
                    elem.appendChild(iframe);
                    DisableShareBtn(option);
                    flag=false;
                    break; 
                
                default:
                    // code...
                    flag=true;
                    break;
        }
    }
    if (!flag) {
        jidHandler(""); 
        // remove jid only if we can't handle it in backend terminal
    }
    return flag;
}

// changes
export const getSelectValue = () => {
    // body...
    const optionMenu = document.getElementById("optionMenu");
    if(optionMenu!==null) {
        const option = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement).value;
        return option;
    }
    return null;
}

// compiler/repl args
function getCompilerArgs() {
    // body...
    const compiler_flags = (document.getElementById("compiler_flags") as HTMLInputElement).value;
    if(compiler_flags!==null) {
        return compiler_flags;
    }
    return "";
}

// Env Variables
function getEnvVars() {
    // body...
    const env_flags = (document.getElementById("env_flags") as HTMLInputElement).value;
    if(env_flags!==null) {
        return env_flags;
    }
    return "";
}

function fetchEditorContent() {
    var editor: any = window["editor"];
    if( editor.env && editor.env.editor && editor.env.editor.getValue && (typeof(editor.env.editor.getValue) === "function")) {
        return btoa(unescape(encodeURIComponent(editor.env.editor.getValue())));
    }
    return "";
}

function fetchEditorFileName() {
    var editor: any = window["editor"];
    if( editor.env && editor.env.filename ) {
        return editor.env.filename;
    }
    return "";
}

function updatePayload(eventname="", debug=false) {
    var pload: Object = {
                                "test":"test",
                        };
    if (eventname == "optionrun") {
        pload[IdeLangKey] = getSelectValue();
        pload[IdeContentKey] = fetchEditorContent();
    }
    if (debug === true) {
        pload[CompilerOptionKey] = "debug";
    }
    pload[IdeFileNameKey] = fetchEditorFileName();
    pload[CompilerFlagsKey] = getCompilerArgs();
    pload[EnvFlagsKey] = getEnvVars();
    return pload;
}

export class GottyTerminal {
    elem: CustomHTMLElement;
    term: Terminal;
    wt: WebTTYFactory;
    closer: Icallback;
    factory: ConnectionFactory;
    debug: boolean;
    ismaster: boolean;
    gotty_auth_token: string;
    gotty_term: string

    activateDisplay() {
        this.elem.classList.remove('inactive');
        this.elem.classList.add('active');
    }

    deactivateDisplay() {
        this.elem.classList.remove('active');
        this.elem.classList.add('inactive');
    }

    getID() : string {
        // return id of this terminal
        if (this.elem.id) {
            return this.elem.id;
        }
        return "terminal";
    }

    setTabTitle(title: string) {
        this.term.setTabTitle(title);
    }

    publishDB(type: string, data: any) {
        try {
            this.wt.dboutput(type, data);
        } catch (error) {
            console.log("Error: publishing data to DB: ", error)
        }
    }

    spawnGotty(option: string|null, eventname: string="optionchange") {
        console.log("option caught: ",option);
        if (!handleTerminalOptions(this.elem, option, eventname)) {
                return;
        }
        if (this.gotty_term == "hterm") {
            this.term = new Hterm(this.elem);
        } else {
            this.term = new Xterm(this.elem);
        }
            
        if (option !== null) {
            if (this.ismaster) {
                const httpsEnabled = window.location.protocol == "https:";
                const url = (httpsEnabled ? 'wss://' : 'ws://') + window.location.host + '/ws' + '_' + option;
                let args = window.location.search;
                let args2 = '';
                if ( eventname=="optionchange" && option && option2args[option] && option2args[option] !== undefined ) {
                    // only optionchange args here
                    args2 = option2args[option];
                    if (args==="") {
                        args2='?'+args2;
                    } else {
                        args2='&'+args2;
                    }
                }
                args +=args2
                let ft = new FireTTY(this.term, this.ismaster, this.elem.isprimary);
                this.factory = new ConnectionFactory(url, protocols);
                let payload = updatePayload(eventname, this.debug);
                this.wt = new WebTTY(this.term, this.factory, ft, payload, args, this.gotty_auth_token);
                this.debug = false;
            } else {
                // for slaves when sharing REPLs
                this.wt = new FireTTY(this.term, this.ismaster, this.elem.isprimary);
            }
            this.closer = this.wt.open();
            console.log("webtty created:");
        }
    }

    constructor(elem: CustomHTMLElement, gotty_term: string, gotty_auth_token: string, isprimary: boolean=false) {
        this.elem = elem;
        this.elem.isprimary = isprimary;
        this.gotty_auth_token = gotty_auth_token;
        this.gotty_term = gotty_term;
        this.ismaster =  isMaster();
        this.debug = false;

        this.elem.addEventListener("optiondebug", () => {
            this.debug = true; // enable debug
            const event = new CustomEvent('optionrun', {
                              detail: { optionT: "optiondebug"}
                            });
            elem.dispatchEvent(event);
        });

        this.elem.addEventListener("optionchange", () => {
            console.log("event caught: change");
            const option = getSelectValue();
            console.log("option caught: ",option);
            if (option && unhandledLanguages.indexOf(option) !== -1) {
                this.Cleanup(true, true);
                // want to keep the callbacks to recieve further notifications on firebase
            } else {
                this.Cleanup(true, false);
            }
            setTimeout(() => {
                // timeout between two events
                this.spawnGotty(getSelectValue());
            }, 500);
        });

        //compile and run from editor
        elem.addEventListener("optionrun", (e) => {
            console.log("event caught: optionrun");
            if(this.ismaster) {
               this.Cleanup(true, false);    
            } else {
                    //wait till optionrun is dispatched to master
                    setTimeout(() => {
                        this.Cleanup(false, false); 
                    }, 100);
            }
            setTimeout(() => {                
                // timeout between two events
                this.spawnGotty(getSelectValue(), "optionrun");
            }, 500);
        });
    };

    Cleanup(keepdb: boolean, keepdbcallbacks: boolean) {
        try {
            let args : CloserArgs = {
                keepdb: keepdb,
                keepdbcallbacks: keepdbcallbacks
            };
            console.log("closing connection with args: ", args);
            this.closer(args);
            this.term.close();
        } catch (error) {
            console.error('Error occurred while Cleanup gottyterm: ', error);
        }
    };
};
