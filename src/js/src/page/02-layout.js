// Workspace layout: maximize, the editor and terminal split (show, hide, rotate), editor content and file saving, reconnect.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.ToggleFunction = ToggleFunction;
window.updateEditorContent = updateEditorContent;
window.updateEditorFileChip = updateEditorFileChip;
window.SaveSelectedNodeToFile = SaveSelectedNodeToFile;
window.ToggleEditor = ToggleEditor;
window.syncEditorLayoutUI = syncEditorLayoutUI;
window.ToggleRotateEditor = ToggleRotateEditor;
window.ToggleReconnect = ToggleReconnect;

function ToggleFunction() {
    // Maximize or restore the workspace: the app bar, files, editor and terminals.
    var shell = get("#ide-shell") || get(".terminal__row");
    if (!shell) return;
    var on = !shell.classList.contains("is-maximized");
    shell.classList.toggle("is-maximized", on);
    document.body.classList.toggle("ide-maximized", on);
    var btn = get("#togglescreen-button");
    if (btn) btn.setAttribute("aria-pressed", on ? "true" : "false");
    var label = get("#togglescreen-label");
    if (label) label.textContent = on ? "Restore" : "Maximize";
    window.dispatchEvent(new Event('resize'));
}

window.einst = null;
window.direction = null;
// process dark and bright theme
window.processtheme = () => {
  var selectedOption = $('#select-theme').find(':selected');
  var themedetected = selectedOption.closest('optgroup').attr('label');
  
  if (themedetected === 'Bright') {
      console.log('Bright theme selected.');
      // want to add dark background for buttons for tooltip only if direction is vertical 
      if (direction==='vertical') $('.fullscreen-toggle').addClass('rev-accent-background');
  } else if (themedetected === 'Dark') {
      console.log('Dark theme selected.');
      $('.fullscreen-toggle').removeClass('rev-accent-background');
  }
};

// updates editor content by ID
function updateEditorContent(cmd="", content="/* Welcome to openrepl! */", forceupdate=false) {
    if(!forceupdate && window[CMD_KEY]===cmd) {
      // no need to update as this is not an optionchange
      console.log("no change in command: ",cmd)
      return;
    }

    var nodename = "";
    var nodetype = "";
    var editor = window["editor"];
    // code to get the selected node
    var tree = $('#file-browser').jstree(true);
    if (tree) {
      var sel = tree.get_selected();
      if (sel.length > 0) { 
        nodename = sel[0];
        nodetype = tree.get_type(sel);
      }
    }

    if (!forceupdate) {
        if (nodetype=="file") {
          // a file is already selected in browser and this is not a force update, so return without updating editor content.
          console.log(" A file is already selected: "+nodename+" skipping editor update.");
          return;
        }
    }
    
    if( editor.env && editor.env.editor && editor.env.editor.getValue && (typeof(editor.env.editor.setValue) === "function")) {
        if (isMaster()) {
          //master (-1 puts the cursor at the start instead of selecting everything)
          editor.env.editor.setValue(content, -1);
        } else {
          //its a slave preserve the content
          content = editor.env.editor.getValue();
        }
        if (nodetype=="file") {
          editor.env.filename = nodename;
        } else {
          editor.env.filename = ""; // unset the filename, for folders, so that we can run test codes from editor as usual.
        }
    }
    window[CONTENT_KEY] = content; 
    if (cmd !== "") {
      window[CMD_KEY] = cmd;
    }
    updateEditorFileChip();
}

// Shows the name of the file open in the editor (or "untitled").
function updateEditorFileChip() {
    var chip = get("#editor-filename");
    if (!chip) return;
    var el = document.getElementById("editor");
    var name = (el && el.env && el.env.filename) ? String(el.env.filename).split("/").pop() : "";
    chip.textContent = name || "untitled";
    chip.title = (el && el.env && el.env.filename) ? el.env.filename : "Not saved to a file. Pick a file in Files to save with Ctrl+S.";
}

function SaveSelectedNodeToFile(oldSelectedNodeId, errcallback=null) {
    // save the old file if its a file
    let type =  $('#file-browser').jstree(true).get_node(oldSelectedNodeId).type;
    if (type === "file") {
      var base64EncodedString=btoa(GetEditorContent());
      $.ajax({
        url: preprocessurl("/ws_filebrowser?q=save&filepath="+oldSelectedNodeId),
        method: "POST",
        processData: false,
        data: base64EncodedString,
        contentType: "application/octet-stream"
      }).done(function(data) {
        // Handle successful response
        console.log("File Saved successfully: "+oldSelectedNodeId);
      }).fail(function(xhr, status, error) {
        // Handle error
        console.log("Failed to save file: "+oldSelectedNodeId, status, error);
        notify((xhr.responseText || error || "Please try again."), { type: "error", title: "Couldn't save " + String(oldSelectedNodeId).split("/").pop() });
        if (typeof(errcallback)==="function") {
          errcallback();
        }
      });
    }
}

function ToggleEditor() {
    if (direction===null) {
      // first time
      if (ismob()) {
        direction = 'vertical';
      } else {
        direction = 'horizontal'
      }
      
    }
    var TermElement = get("#terminal-div");
    var ideElement =  get("#ide");
    var editorbtn = get("#editor-button");
    if(editorbtn !==undefined && editorbtn !== null ) {
      editorbtn.classList.toggle("toggle-accent-color");
    }
    if (einst === null) {
      let default_right = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--gutter-right')) || 0;
      let default_prevMouseX = 0;
      let sizes = [55,45];
      if (direction==='vertical') sizes=[65,35];
      einst = Split(['#ide', '#terminal-div'], {
        gutterSize: 3,
        sizes: sizes,
        direction: direction,
        onDrag: function(event) {
          let currentMouseX = event[0] || 0;
          let prevMouseX = default_prevMouseX;
          let gutterright = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--gutter-right')) || 0;
          
          let direction = (prevMouseX < currentMouseX) ? 'right':'left';
          if (direction==='left') {
            document.documentElement.style.setProperty('--gutter-rotate', '-90deg');
          } else {
            document.documentElement.style.setProperty('--gutter-rotate', '90deg');
          }
          default_prevMouseX = currentMouseX;
          gutterright++;
          if (gutterright > 20) {
            // rotate
            gutterright = 0;
          }
          document.documentElement.style.setProperty('--gutter-right', `${gutterright}px`);
        },
        onDragEnd: function (e) {
          console.log("onDragEnd", e);
          default_prevMouseX = e[0] || 0;
          document.documentElement.style.setProperty('--gutter-rotate', '90deg'); // right at the end
          document.documentElement.style.setProperty('--gutter-right', `${default_right}px`);
        } 
      });
      if (ideElement !==undefined && ideElement !== null) {
        ideElement.style.display = "flex";
      }
    } else {
      einst.destroy();
      if (ideElement !==undefined && ideElement !== null) {
        ideElement.style.display = "none";
      }
      einst = null;
    }
    syncEditorLayoutUI();
}

// Reflects the editor state (shown or hidden, side by side or stacked) in the
// workspace controls. Called after every ToggleEditor/ToggleRotateEditor.
function syncEditorLayoutUI() {
    var hidden = einst === null;
    var split = get("#ide-split");
    if (split) split.classList.toggle("is-vertical", direction === 'vertical');
    var showBtn = get("#show-editor-button");
    if (showBtn) showBtn.hidden = !hidden;
    var rot = get("#rotate-button-label");
    if (rot) rot.textContent = direction === 'vertical' ? "Side by side" : "Stacked";
    document.body.classList.toggle("editor-hidden", hidden);
    setTimeout(function () { window.dispatchEvent(new Event('resize')); }, 50);
}
/* rotates direction of editor/REPL vertical <-> horizontal */
function ToggleRotateEditor() {
  if (direction===null) {
    // first time
    if (ismob()) {
      direction = 'vertical';
    } else {
      direction = 'horizontal';
    }
  } else {
    //toggle direction
    if (direction=='vertical') {
      direction = 'horizontal';
    } else if (direction=='horizontal') {
      direction = 'vertical';
    }
  }
  if (einst) {
    einst.destroy();
    einst = null; // destroy and reset splitter
  }
  ToggleEditor();
}

function ToggleReconnect() {
    // The execution node is away and the countdown is running (11-terminal-state.js).
    if (window.awayWaitMs && window.awayWaitMs() > 0) return;
    const optionMenu = get("#optionMenu");
    if(optionMenu!==undefined) {
        const option = get(".list", optionMenu);
        if (option!==undefined) {
          option.dispatchEvent(new Event("change"));
        }
    }
}

export {};  // an ES module: strict mode, bundled by webpack
