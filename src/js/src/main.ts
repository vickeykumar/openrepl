import { Hterm } from "./hterm";
import { Xterm } from "./xterm";
import { Terminal, WebTTY, protocols } from "./webtty";
import { ConnectionFactory } from "./websocket";

// @TODO remove these
declare var gotty_auth_token: string;
declare var gotty_term: string;




function handleTerminalOptions(elem, option) {
    if (option!==null && elem!==null) {
        var javaframe = elem.getElementsByClassName("java")[0];
        if (javaframe !== undefined) {
            elem.removeChild(javaframe);
        }
        switch (option) {
                case "java":
                    // code...
                    var iframe = document.createElement("IFRAME");
                    iframe.setAttribute("class","java");
                    iframe.setAttribute("src","http://www.javarepl.com/embed.html");
                    iframe.setAttribute("style","width: inherit; height: inherit; border: 0px;");
                    elem.appendChild(iframe);
                    return false;
                
                default:
                    // code...
                    return true;
        }
    }
    return true;
}

// changes
function getSelectValue() {
    // body...
    const optionMenu = document.getElementById("optionMenu");
    if(optionMenu!==null) {
        const option = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement).value;
        return option;
    }
    return null;
}
const optionMenu = document.getElementById("optionMenu");
if(optionMenu!==null) {
    const SelectOption = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement);
    if (SelectOption !== null) {
        SelectOption.addEventListener("change", ActionOnChange);
    }
}

export function ActionOnChange() {
    // body...
    const elem = document.getElementById("terminal");
    if (elem !== null) {
        var event = new Event('optionchange');
        elem.dispatchEvent(event);
    };
}

/*
var command = getSelectValue()
if (command===null) {
    command = ""
} else {
    command = "_" + command 
}
*/

const elem = document.getElementById("terminal")
if (elem !== null) {
    var term: Terminal;
    if (gotty_term == "hterm") {
        term = new Hterm(elem);
    } else {
        term = new Xterm(elem);
    }
    const httpsEnabled = window.location.protocol == "https:";
    const url = (httpsEnabled ? 'wss://' : 'ws://') + window.location.host + window.location.pathname + 'ws_c';
    const args = window.location.search;
    var factory = new ConnectionFactory(url, protocols);
    var wt = new WebTTY(term, factory, args, gotty_auth_token);
    var closer = wt.open();
    console.log("webtty created: ");

    window.addEventListener("unload", () => {
        console.log("closing connection")
        closer();
        term.close();
    });
    elem.addEventListener("optionchange", () => {
        console.log("event caught: change");
        var event = new Event('unload');
        window.dispatchEvent(event);
        setTimeout(function(){                // timeout between two events
            //var term: Terminal;
            const option = getSelectValue();
            console.log("option caught: ",option);
            if (!handleTerminalOptions(elem, option)) {
                return;
            }
            if (gotty_term == "hterm") {
                term = new Hterm(elem);
            } else {
                term = new Xterm(elem);
            }
            
            if (option !== null) {
                const httpsEnabled = window.location.protocol == "https:";
                const url = (httpsEnabled ? 'wss://' : 'ws://') + window.location.host + window.location.pathname + 'ws'+ '_' + option;
                const args = window.location.search;
                factory = new ConnectionFactory(url, protocols);
                wt = new WebTTY(term, factory, args, gotty_auth_token);
                closer = wt.open();
                console.log("webtty created:");
                /*window.addEventListener("unload", () => {
                    console.log("closing connection")
                    closer();
                    term.close();
                });*/
            }
        }, 500);
    });
};
