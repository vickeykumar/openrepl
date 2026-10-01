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
    };
}

// file extension shown beside each language in the tab menu
const LANG_EXT: {[value: string]: string} = {
    "c": ".c", "cpp": ".cpp", "go": ".go", "yaegi": ".go", "java": ".java", "javascript": ".js",
    "ts-node": ".ts", "jq-repl": ".json", "node": ".mjs", "python": ".py", "python2.7": ".py",
    "ipython3": ".py", "irb": ".rb", "perli": ".pl", "bash": ".sh", "tclsh": ".tcl",
    "evcxr": ".rs", "sqlite3": ".sql", "rappel": ".asm"
};
const TAB_MENU_MARGIN = 8; // keep the menu this far inside the window
var tabMenuOpener: HTMLElement | null = null; // element to give focus back to

function tabMenuItems(menu: HTMLElement): HTMLElement[] {
    return Array.prototype.slice.call(menu.querySelectorAll('button.ctx-item:not(:disabled)'));
}

function hideTabContextMenu(restoreFocus: boolean = false) {
    const contextMenu = document.getElementById('tabContextMenu') as HTMLElement | null;
    if (contextMenu === null || contextMenu.hidden) {
        return;
    }
    contextMenu.hidden = true;
    if (restoreFocus && tabMenuOpener !== null) {
        tabMenuOpener.focus();
    }
    tabMenuOpener = null;
}

function showTabContextMenu(event: MouseEvent) {
    const contextMenu = document.getElementById('tabContextMenu') as HTMLElement | null;
    const picker = document.getElementById('optionlist') as HTMLSelectElement | null;
    if (contextMenu === null || picker === null) {
        return;
    }
    event.preventDefault();
    // tick the language the picker shows now
    contextMenu.querySelectorAll('#tabLangList .ctx-item').forEach((item) => {
        item.setAttribute('aria-checked', (item as HTMLElement).dataset.value === picker.value ? 'true' : 'false');
    });
    const target = event.target as HTMLElement;
    tabMenuOpener = (target.closest('.tab') || document.querySelector('#terminal-tabs .tab.active')) as HTMLElement | null;

    // measure first, then place it inside the window (position: fixed, so scrolling the page doesn't matter)
    contextMenu.style.maxHeight = `${window.innerHeight - 2 * TAB_MENU_MARGIN}px`;
    contextMenu.hidden = false;
    let x = event.clientX;
    let y = event.clientY;
    if (x === 0 && y === 0 && tabMenuOpener !== null) {
        // opened from the keyboard: sit under the tab
        const rect = tabMenuOpener.getBoundingClientRect();
        x = rect.left;
        y = rect.bottom;
    }
    const width = contextMenu.offsetWidth;
    const height = contextMenu.offsetHeight;
    const left = Math.max(TAB_MENU_MARGIN, Math.min(x, window.innerWidth - width - TAB_MENU_MARGIN));
    const top = y + height + TAB_MENU_MARGIN > window.innerHeight ? Math.max(TAB_MENU_MARGIN, y - height) : y;
    contextMenu.style.left = `${left}px`;
    contextMenu.style.top = `${top}px`;

    const current = contextMenu.querySelector('.ctx-item[aria-checked="true"]') as HTMLElement | null;
    (current || tabMenuItems(contextMenu)[0]).focus();
}

var primaryterm: GottyTerminal; // primary terminal tab
const termelem = document.getElementById("terminal") as CustomHTMLElement;
const isprimary : boolean = true;
const launcher = (firebaseconfig: any) => {
    InitializeApp(firebaseconfig);
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

    // fill the Tab context menu: Reconnect, and one entry per language in the picker
    const originalOption = document.getElementById('optionlist') as HTMLSelectElement;
    const contextMenu = document.getElementById('tabContextMenu') as HTMLElement;
    const langList = document.getElementById('tabLangList') as HTMLElement;
    const reconnect = document.getElementById('tabrefresh') as HTMLElement;
    if (originalOption && contextMenu && langList && reconnect) {
        reconnect.addEventListener('click', function() {
            hideTabContextMenu();
            (window as any).ToggleReconnect();
        });
        Array.prototype.slice.call(originalOption.options).forEach((option: HTMLOptionElement) => {
            const item = document.createElement('button');
            item.type = 'button';
            item.className = 'ctx-item';
            item.setAttribute('role', 'menuitemradio');
            item.setAttribute('aria-checked', 'false');
            item.dataset.value = option.value;
            const check = document.createElement('i');
            check.className = 'ctx-i ctx-i-check';
            check.setAttribute('aria-hidden', 'true');
            const label = document.createElement('span');
            label.className = 'ctx-item__label';
            label.textContent = option.text;
            const ext = document.createElement('span');
            ext.className = 'ctx-item__ext';
            ext.textContent = LANG_EXT[option.value] || '';
            item.append(check, label, ext);
            item.addEventListener('click', function() {
                const changed = originalOption.value !== option.value;
                console.log("contextmenu selected value: ", option.value);
                hideTabContextMenu(true);
                if (changed) {
                    originalOption.value = option.value;
                    originalOption.dispatchEvent(new Event("change"));
                }
            });
            langList.appendChild(item);
        });

        // arrow keys move through the items, Esc closes and returns to the tab
        contextMenu.addEventListener('keydown', function(e: KeyboardEvent) {
            const items = tabMenuItems(contextMenu);
            const at = items.indexOf(document.activeElement as HTMLElement);
            let next = -1;
            if (e.key === 'ArrowDown') { next = (at + 1) % items.length; }
            else if (e.key === 'ArrowUp') { next = (at - 1 + items.length) % items.length; }
            else if (e.key === 'Home') { next = 0; }
            else if (e.key === 'End') { next = items.length - 1; }
            else if (e.key === 'Escape') { e.preventDefault(); hideTabContextMenu(true); return; }
            else if (e.key === 'Tab') { hideTabContextMenu(); return; }
            if (next >= 0) {
                e.preventDefault();
                items[next].focus();
            }
        });

        // close when clicking elsewhere, or when the window changes under it
        document.addEventListener('click', function(e) {
            if (!contextMenu.hidden && !contextMenu.contains(e.target as Node)) {
                hideTabContextMenu();
            }
        });
        window.addEventListener('resize', function() { hideTabContextMenu(); });
        window.addEventListener('blur', function() { hideTabContextMenu(); });
        document.addEventListener('scroll', function(e) {
            // only the page moving closes it; the terminal and the language list scroll on their own
            if (e.target === document) {
                hideTabContextMenu();
            }
        }, true);
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
        const notify = (window as any).notify;
        if (typeof notify === "function") {
            notify("You can have up to " + MAX_TABS + " terminals open. Close one to open another.", { type: "info" });
        } else {
            window.alert("Maximum number of allowed connections reached.");
        }
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

