import { GottyTerminal, getSelectValue, CustomHTMLElement } from "./gotty";
import { setEventHandler } from "./webtty";
import { InitializeApp, setTabEventHandler } from "./firetty";

/*
next task is to make this modular in to another class as GottyTerm
- indicates a single gotty instance , will be use full to create multiple tabs
*/

const MAX_TABS=5;

// @TODO remove these
declare var gotty_auth_token: string;
declare var gotty_term: string;



const optionMenu = document.getElementById("optionMenu");
if(optionMenu!==null) {
    const SelectOption = (optionMenu.getElementsByClassName("list")[0] as HTMLSelectElement);
    if (SelectOption !== null) {
        SelectOption.addEventListener("change", ActionOnChange);
    }
}

export function ActionOnChange(e: any) {
    const target = e.target as HTMLSelectElement;
    const selectedValue = target.value;
    let isSilent = e.detail && e.detail.silent;
    if (isSilent) {
        // its a silent event
        console.log("its a silent event, return...");
        return;
    }
    // select one active terminal and fire event
    const elem = document.querySelector(".terminal.active") as CustomHTMLElement;
    if (elem !== null) {
        var event = new Event('optionchange');
        elem.dispatchEvent(event);
        const taboption = document.getElementById('tabOptionMenu') as HTMLSelectElement;
        if (taboption) {
            // no events here, cz its triggered globally
            taboption.value = selectedValue;
        }
    };
}

function showTabContextMenu(event) {
    event.preventDefault();
    const contextMenu = document.getElementById('tabContextMenu') as HTMLDivElement;
    contextMenu.style.left = `${event.clientX}px`;
    contextMenu.style.top = `${event.clientY}px`;
    contextMenu.style.display = 'block';
}

InitializeApp();

var primaryterm: GottyTerminal; // primary terminal tab
const termelem = document.getElementById("terminal") as CustomHTMLElement;
const isprimary : boolean = true;
const launcher = () => {
    if (termelem !== null) {
        primaryterm = new GottyTerminal(termelem, gotty_term, gotty_auth_token, isprimary); // create a gotty terminal instance and register for the callbacks
        // start the gotty terminal instance on the element #terminal
        primaryterm.spawnGotty(getSelectValue());
        // u ned to close this gottyterm before unload
        // save this term in main tab
        let firsttab = document.querySelector('#terminal-tabs .tab') as CustomHTMLElement;
        firsttab.gottyterm = primaryterm;
        termelem.tab=firsttab; // save the tab correspondin to this term element
        firsttab.addEventListener('click', (event : MouseEvent) => {
            const target = event.target as HTMLElement;
            console.log("inside click for : ", target);
            const activeTab = document.querySelector('.tab.active') as CustomHTMLElement;
            if (activeTab) {
                if (activeTab==firsttab) {
                    console.log("target already active.");
                    return;
                }
                activeTab.classList.remove('active');
                activeTab.gottyterm.deactivateDisplay();
            }

            // send from primary tab always, (as of now).
            primaryterm.publishDB("tab", {
                op: "click",
                termid: primaryterm.getID(),
            });

            firsttab.classList.add('active');
            firsttab.gottyterm.activateDisplay();
        });
    };

    window.addEventListener("unload", (e: any) => {
        console.log("Window unload event: closing connections");
        const tabsContainer = document.getElementById('terminal-tabs') as HTMLElement;
        const tabs = tabsContainer.querySelectorAll('.tab');
        for (let i = 0; i < tabs.length; i++) {
            const tab = tabs[i] as CustomHTMLElement;
            try {
                // Close and Cleanup the terminal instance
                tab.gottyterm.Cleanup(false, false);
                console.log("cleaning up: ",tab.gottyterm.elem.id);
            } catch (error) {
                console.error('Error occurred while cleaning up terminal:', error);
            }
        }
    });

    // create Tab context menu and add it to body
    const originalOption = document.getElementById('optionlist') as HTMLSelectElement;
    const contextMenu = document.getElementById('tabContextMenu') as HTMLElement;
    if (originalOption && contextMenu) {
        contextMenu.style.position = 'absolute';
        contextMenu.style.display= 'none';
        const optionMenu = originalOption.cloneNode(true) as HTMLSelectElement;
        optionMenu.id = 'tabOptionMenu';
        optionMenu.size = 7;
        optionMenu.value=originalOption.value;
        contextMenu.appendChild(optionMenu);
        // Add event listener to synchronize option selection
        optionMenu.addEventListener('change', function() {
            console.log("contextmenu selected value: ", this.value);
            originalOption.value = this.value;
            // Remove the context menu after selection
            contextMenu.style.display= 'none';
            originalOption.dispatchEvent(new Event("change"));
        });

        // Remove the context menu if clicked outside
        document.addEventListener('click', function(e) {
            if (!contextMenu.contains(e.target as Node) && contextMenu.style.display=='block') {
                contextMenu.style.display= 'none';
            }
        });
    }

    const tabsContainer = document.getElementById('terminal-tabs') as HTMLElement;
    // Add event listener to tabcontainer show context menu on right-click
    tabsContainer.addEventListener('contextmenu', showTabContextMenu);

}; //end of launcher

function addTab(terminalid:string="", skipdb:boolean=false) {
    const tabsContainer = document.getElementById('terminal-tabs') as HTMLElement;
    const tabs = tabsContainer.querySelectorAll('.tab');
    const tabids = Array.prototype.slice.call(tabs).map(tab => tab.gottyterm.elem.id);
    console.log("Existing terminal ids: ", tabids);
    const tabCount = tabs.length;

    if (tabCount >= MAX_TABS) {
        window.alert("Maximum number of allowed connections reached.");
        return;
    }
    // now find a sutable terminal id
    if (terminalid == "") {
        let id=1;
        terminalid = `terminal-${id}`;
        while (tabids.indexOf(terminalid) !== -1) {
            // already present, choose another one
            id ++
            terminalid = `terminal-${id}`;
        }
        console.log("got a terminal id: ", terminalid);
    }

    // disable old tab
    const activeTab = document.querySelector('.tab.active') as CustomHTMLElement;
    if (activeTab) {
        activeTab.classList.remove('active');
        activeTab.gottyterm.deactivateDisplay();
        // hide previous terminal
    }

    

    const newTab = document.createElement('div') as HTMLDivElement & { gottyterm: any };
    newTab.classList.add('tab', 'active');
    newTab.innerHTML += `<span class="tab-title">${terminalid}</span>`;
    newTab.innerHTML += '<button class="close-tab" onclick="gotty.closeTab(event)">×</button>';

    // last add button should be safe
    const lastAddButton = tabsContainer.querySelector('.add-tab:last-of-type') as HTMLElement;
    tabsContainer.insertBefore(newTab, lastAddButton);


    
    // Initialize new gotty terminal element
    const terminalContainer = document.createElement('div') as HTMLDivElement & { tab: any; gottyterm: any; isprimary:boolean };
    terminalContainer.classList.add('terminal', 'active');
    terminalContainer.id = terminalid;
    terminalContainer.tab = newTab; // save for later reference
    const parentcontainer = tabsContainer.parentNode as HTMLElement;
    parentcontainer.appendChild(terminalContainer);
    
    try {
        newTab.gottyterm = new GottyTerminal(terminalContainer, gotty_term, gotty_auth_token);
        newTab.gottyterm.spawnGotty(getSelectValue());
        if (!skipdb) {
            // skip pushdb if its a local event
            primaryterm.publishDB("tab", {
                op: "add",
                termid: newTab.gottyterm.getID(), 
            });
        }
    } catch (error) {
        console.error('Error occurred while adding tab: ', error);
    }
    

    newTab.addEventListener('click', (event: any) => {
        let skipdb = false;
        if (event.detail && event.detail.skipdb) {
            skipdb = true;
        }
        const target = event.target as HTMLElement;
        console.log("inside click: ", target);
        const activeTab = document.querySelector('.tab.active') as CustomHTMLElement;
        if (activeTab) {
            if (activeTab==target) {
                console.log("target already active.");
                return;
            }
            activeTab.classList.remove('active');
            activeTab.gottyterm.deactivateDisplay();
        }
        if (!skipdb) {
            primaryterm.publishDB("tab", {
                op: "click",
                termid: newTab.gottyterm.getID(),
            });
        }
        newTab.classList.add('active');
        newTab.gottyterm.activateDisplay();
    });
}

function closeTab(event: any, skipdb:boolean=false) {
    const activeTab = document.querySelector('.tab.active') as CustomHTMLElement;
    const target = event.target as HTMLElement;
    const tab = target.parentNode as CustomHTMLElement;
    console.log(tab, event);
    
    let nextactive = activeTab;
    // if no valid next active found
    if ((!nextactive) || tab==activeTab) {
        // active is going,  make just next guy active
        nextactive = tab.nextElementSibling as CustomHTMLElement;
        if (nextactive.id=="add-tab") {
            // make first guy then if last node reached
            nextactive = document.querySelector('.tab') as CustomHTMLElement;
        }
    }

    

    try {
        // Close and Cleanup the terminal instance
        tab.gottyterm.Cleanup(false, false);
        tab.gottyterm.deactivateDisplay();
    } catch (error) {
        console.error('Error occurred while cleaning up terminal:', error);
    }
    let id = tab.gottyterm.getID();
    tab.gottyterm.elem.remove();
    tab.gottyterm=null;
    // Remove the tab and its associated content
    tab.remove();
    if (!skipdb) {
        // send from primary tab always
        primaryterm.publishDB("tab", {
            op: "close",
            termid: id,
        });
    }
    console.log("cleanup done, clicking nextactive: ", nextactive);
    if (nextactive) {
        setTimeout(function() {
            nextactive.click();
        }, 100);
    }
}

interface TabEventData {
    op: string;
    termid: string;
    title?:string;
}

setTabEventHandler((eventdata: TabEventData) => {
    console.log("Tab Event data recieved in main: ", eventdata);
    let elem: CustomHTMLElement | null;
    switch (eventdata.op) {
    case "add":
        addTab(eventdata.termid, true);
        // add a tab with termid
        break;
    case "close":
        elem = document.getElementById(eventdata.termid) as CustomHTMLElement;
        if (elem) {
            const tab =  elem.tab as HTMLElement;
            if (tab) {
                const targetnode = tab.querySelector('.close-tab') as HTMLElement;
                if (targetnode) {
                    // finally found the target close button i have to click
                    // Construct a custom event object with the target node as the target
                    const customEvent = {
                        target: targetnode
                    };

                    closeTab(customEvent, true);
                }
            }
        }
        break;
    case "click":
        elem = document.getElementById(eventdata.termid) as CustomHTMLElement;
        if (elem) {
            const tab =  elem.tab as HTMLElement;
            if (tab) {
                const customMouseEvent = new CustomEvent('click', {
                    detail: { skipdb: true }, // Include custom data in the detail property
                });
                tab.dispatchEvent(customMouseEvent);
            }
        }
        break;
    case "title":
        elem = document.getElementById(eventdata.termid) as CustomHTMLElement;
        if (elem) {
            const tab =  elem.tab as CustomHTMLElement;
            if (tab && eventdata.title) {
                tab.gottyterm.setTabTitle(eventdata.title);
            }
        }
        break;
    default:
        console.log("unhandled event recieved in tabhandler.");
    }
});

// exported to be used outside bundle for other tasks
export { setEventHandler, launcher, addTab, closeTab};

