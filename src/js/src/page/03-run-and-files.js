// Language from the URL, the tour, Run and Debug, editor download and upload, and file uploads to the workspace.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.LoadOptionFromUrl = LoadOptionFromUrl;
window.StartTour = StartTour;
window.CompileandRun = CompileandRun;
window.RunandDebug = RunandDebug;
window.GetEditorContent = GetEditorContent;
window.DownloadEditor = DownloadEditor;
window.UploadEditor = UploadEditor;
window.digestMessage = digestMessage;
window.uploadFile = uploadFile;

function LoadOptionFromUrl() {
	var searchParams = new URL(location.href.toLowerCase()).searchParams;
	// ?repl=python, or the language of a language page such as /python (T12)
	var repl = searchParams.get("repl") || (window.OPENREPL_PAGE && window.OPENREPL_PAGE.repl) || null;
    var optionMenu = $('#optionlist')[0];
  	if (optionMenu) {
    	for (var i = 0; i < optionMenu.length; i++){
      		var option = optionMenu.options[i];
		var lang = option.text.trim().toLowerCase();
      		if ((repl && option.getAttribute("value").toLowerCase() === repl) || (searchParams.get(lang)!==null && searchParams.get(lang)!==undefined)) {
				//option found
				optionMenu.value = option.value;
				//optionMenu.dispatchEvent(new Event("change"));
				return;
      		}
    	}
  	}
}

function StartTour() {
  // The tour follows the refreshed layout (T3 made it opt-in; updated for T4-T10).
  var q = function (sel) { return document.querySelector(sel); };
  var filesVisible = q('#files-panel') && q('#files-panel').offsetParent !== null;
  var steps = [
    { title: 'Welcome to OpenREPL', intro: 'A one-minute tour of the workspace. Press Esc at any time to leave.' },
    { element: q('#lang-chips'), intro: 'Pick a language here, or use the picker in the workspace. A fresh REPL starts in its own sandbox.' },
    { element: q('#optionlist-button') || q('#optionlist'), intro: 'All 19 languages are in this picker.' },
    { element: q('#terminal-div'), title: 'The REPL', intro: 'Type a line and press Enter to see the result straight away. Use + to open more terminals.' },
    { element: q('#ide'), title: 'The editor', intro: 'Write longer code here. The status bar has the editor language, theme and font size.' },
    { element: q('.run-split'), title: 'Run', intro: 'Run the editor code with Ctrl+Enter (Cmd+Enter on a Mac). The arrow opens Debug, program arguments and environment variables.' },
    { element: filesVisible ? q('#files-panel') : q('#files-toggle'), title: 'Files', intro: 'Your workspace files. Upload, download, and right-click for more. Guest files are deleted an hour after your last visit.' },
    { element: q('#fork-widget'), title: 'Fork', intro: 'Open a second terminal in the same sandbox, for example to run a server in one and a client in the other.' },
    { element: q('.share-wrap'), title: 'Share', intro: 'Copy a link so someone can watch and type along with you.' },
    { element: q('#genie-button'), title: 'Genie', intro: 'Ask Genie about the code in your editor. It can insert fixes for you.' },
    { element: q('#getting-started'), title: 'Getting started', intro: 'A demo and the commands for the language you picked.' },
    { element: q('#practice_dsa'), title: 'Practice', intro: 'Practice DSA questions with an AI interviewer.' },
    { title: 'That is it', intro: 'Enjoy! If OpenREPL helps you, a star on GitHub helps us too.' }
  ].filter(function (step) { return !('element' in step) || step.element; });
  Promise.all([loadStyleOnce(INTROJS_CSS), loadScriptOnce(INTROJS_JS)]).then(function () {
    introJs().setOptions({ steps: steps }).start();
  }).catch(function () {
    notify("The tour couldn't load. Check your connection and try again.", { type: "error" });
  });
}

function CompileandRun() {
    // The execution node is away and the countdown is running (11-terminal-state.js).
    if (window.awayWaitMs && window.awayWaitMs() > 0) return;
    const termdiv = get("#terminal-div");
    if(termdiv) {
        const allterm = getAll(".terminal.active", termdiv);
        // dispatch event to all active terminals
        for (let i = 0; i < allterm.length; i++) {
            const termElem = allterm[i];
            const computedStyle = getComputedStyle(termElem);
            if (computedStyle.display !== 'none') {
                termElem.dispatchEvent(new Event("optionrun"));
            }
        }
    }
}

function RunandDebug() {
    if (window.awayWaitMs && window.awayWaitMs() > 0) return;
    const termdiv = get("#terminal-div");
    if(termdiv) {
        const allterm = getAll(".terminal.active", termdiv);
        // dispatch event to all active terminals
        for (let i = 0; i < allterm.length; i++) {
            const termElem = allterm[i];
            const computedStyle = getComputedStyle(termElem);
            if (computedStyle.display !== 'none') {
                termElem.dispatchEvent(new Event("optiondebug"));
            }
        }
    }
}

function GetEditorContent() {
  var editor = window["editor"];
  if( editor.env && editor.env.editor && editor.env.editor.getValue && (typeof(editor.env.editor.getValue) === "function")) {
    var content = editor.env.editor.getValue();
    return content;
  }
  return null;
}

function DownloadEditor() {
  var editor = window["editor"];
  var filename = "";
  if (editor.env) {
    filename = editor.env.filename;
  }
  var editorContent = GetEditorContent();
  if( editorContent !== null) {
    if (!(filename)) {
      filename = prompt("Please enter the filename to save");
    }
    if(filename) {
      var blob = new Blob([editorContent], {type: "text/any;charset=utf-8"});
      saveAs(blob, filename);
    }
  }
}

function UploadEditor() {
  var fileToLoad = document.getElementById("fileToLoad").files[0];
  console.log("file to read: ", fileToLoad);
  var editor = window["editor"];
  if( fileToLoad && editor.env && editor.env.editor && editor.env.editor.setValue && (typeof(editor.env.editor.setValue) === "function")) {
    var fileReader = new FileReader();
    fileReader.onload = function(fileLoadedEvent) 
    {
      var textFromFile = "";
      textFromFile = fileLoadedEvent.target.result;
      editor.env.editor.setValue(textFromFile);
      document.getElementById("fileToLoad").value="";
    };
    fileReader.readAsText(fileToLoad, "UTF-8"); 
  }
}

async function digestMessage(message) {
  const msgUint8 = new TextEncoder().encode(message); // encode as (utf-8) Uint8Array
  const hashBuffer = await crypto.subtle.digest("SHA-256", msgUint8); // hash the message
  const hashArray = Array.from(new Uint8Array(hashBuffer)); // convert buffer to byte array
  const hashHex = hashArray
    .map((b) => b.toString(16).padStart(2, "0"))
    .join(""); // convert bytes to hex string
  return hashHex;
}


// jQuery AJAX call to send file to openrepl server
function uploadFile() {
  // Get the file object from the input element
  var file = document.getElementById('fileToLoad').files[0];

  if (file.size > MAX_FILESIZE) {
    notify(file.name + " is over " + (MAX_FILESIZE / 1024 / 1024) + " MB. Choose a smaller file.", { type: "error", title: "Upload failed" });
    return;
  }

  var reader = new FileReader();
  reader.onload = function(event) {
    digestMessage(event.target.result).then((digestHex) => {

      console.log(digestHex)
      var checksum = digestHex;
      // Create a FormData object and append the file and its properties to it
      var formData = new FormData();
      formData.append('file', file, file.name);
      formData.append('checksum', checksum.toString());
      console.log('checksum sent: ', checksum.toString());

      // Send the AJAX request to the server
      $.ajax({
        url: preprocessurl('/upload_file'),
        method: 'POST',
        enctype: 'multipart/form-data',
        data: formData,
        processData: false,
        contentType: false,
        success: function(response) {
          console.log("success: ",response);
          notify("Uploaded " + file.name, { type: "success" });
          // reset
          document.getElementById("fileToLoad").value="";
          $("#loader").fadeOut();
        },
        error: function(xhr, status, error) {
          notify((xhr.responseText || error || "Please try again."), { type: "error", title: "Upload failed" });
          // reset
          document.getElementById("fileToLoad").value="";
          $("#loader").fadeOut();
        }
      });
    });
    // Calculate the checksum of the file using library CryptoJS
    //var checksum = CryptoJS.SHA256(event.target.result);
  
  };
  reader.readAsText(file, "UTF-8");
  $("#loader").fadeIn();
}

export {};  // an ES module: strict mode, bundled by webpack
