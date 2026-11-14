// The Ace editor: set-up, themes, fonts, modes, key bindings and live sharing of the editor through Firebase.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

/* editor App */

$(function() {

    // Initialize Firebase App
    if (firebase.apps.length === 0) {
      firebase.initializeApp(firebaseconfig);
    }
    
    const changeOptionByData = (data="", is_silent=false) => {
      var optionMenu = $('#optionlist')[0];
      if (optionMenu) {
        console.log("changeOptionByData: ",data);
        for (var i = 0; i < optionMenu.length; i++){
          var option = optionMenu.options[i];
          if (option.getAttribute("data-editor") === data) {
            optionMenu.value = option.value;
            const customEvent = new CustomEvent('change', {
                detail: { silent: is_silent }
            });
            // propagate the change event further
            optionMenu.dispatchEvent(customEvent);
            return;
          }
        }
      }
    }

    // Get the editor id, using getExampleRef
    // also sets a global window variable to be used by repl apis.
    var editorId = getExampleRef();
    window.dbpath = editorId;
    console.log("editorId: ",editorId);
    
    // This is the local storage field name where we store the user theme
    // We set the theme per user, in the browser's local storage
    var LS_THEME_KEY = "editor-theme";

    // This function will return the user theme or Monokai (the default)
    function getTheme() {
        try {
            return localStorage.getItem(LS_THEME_KEY) || "ace/theme/monokai";
        } catch (e) {
            return "ace/theme/monokai";
        }
    }
    
    // Select the desired theme of the editor
    $("#select-theme").change(function () {
        // Set the theme in the editor, then the IDE around it follows (21-ide-theme.js)
        var picked = this.value;
        editor.setTheme(picked, function () { if (window.applyIdeTheme) window.applyIdeTheme(editor, picked); });
        
        // Update the theme in the localStorage
        // We wrap this operation in a try-catch because some browsers don't
        // support localStorage (e.g. Safari in private mode)
        try {
            localStorage.setItem(LS_THEME_KEY, this.value);
        } catch (e) {}
    }).val(getTheme());

    // Select the desired fontsize of the editor
    $("#efontSize").change(function () {
        editor.setFontSize(this.value+"px");
    });
    
    // The editor's list has C and C++ as two entries (the picker has two REPLs for them); Ace colours both with its one mode.
    // A record of an older page names that mode ("c_cpp"): it is the language the picker is on, C unless that is C++.
    var aceMode = function (value) { return value === "c" || value === "cpp" ? "c_cpp" : value; };
    var listValue = function (value) { return value === "c_cpp" ? ($("#optionlist").val() === "cpp" ? "cpp" : "c") : value; };

    // Select the desired programming language you want to code in 
    var $selectLang = $("#select-lang").change(function (event) {
        // "Image" and "PDF" are shown, not edited: the file sets them, and they are no language of the editor, so
        // nothing is told to the editor or to the other people of the session
        if (this.value === "image" || this.value === "pdf") return;
        // Check if the silent event
        let is_silent = (event.detail && event.detail.silent) || false;
        console.log("is silent change: ", is_silent, event);
        // Set the language in the Firebase object
        // This is a preference per editor. Not by a collaborator of a session that is over: that would bring back a record of it.
        if (!(window.OwnerAway && window.OwnerAway.active())) currentEditorValue.update({
            lang: {
              data: this.value,
              silent: is_silent // trigger event only when told
            }
        });
        // Set the editor language
        if (editor) {
          editor.getSession().setMode("ace/mode/" + aceMode(this.value));
        }

        //get language from optionmenu
        var optionlang = $('#optionlist option:selected').data('editor');
        if ( optionlang && optionlang !== this.value ) {
          //local change triggered from editor
          //reflect in option menu
          changeOptionByData(this.value, is_silent);
        }
    });

    // Generate a pseudo user id
    // This will be used to know if it's me the one who updated
    // the code or not
    var uid = Math.random().toString();
    var editor = null;
    // Make a reference to the database
    var db = firebase.database();
    
    // Write the entries in the database 
    var editorValues = db.ref("editor_values");
    
    // Get the current editor reference
    var currentEditorValue = editorValues.child(editorId);
    
    // Store the current timestamp (when we opened the page)
    // It's quite useful to know that since we will
    // apply the changes in the future only
    var openPageTimestamp = Date.now();

    var editorInitialized = false;
    const initializeEditorApp = (initialcontent) => {
      if (!editorInitialized) {
        //all init for editor goes here
        editorInitialized = true;
        // Somebody changed the lang. Hey, we have to update it in our editor too!
        var firstLang = true;
        currentEditorValue.child("lang").on("value", function (r) {
            let langdata = r.val();
            // The owner's page opens in its own language (the one chosen last, 32-remember-language.js) and tells the session
            // about it: what the record held when the page opened is from before, and must not replace it. Later changes,
            // by a collaborator, do.
            if (firstLang) {
                firstLang = false;
                if (typeof isMaster !== "function" || isMaster()) return;
            }
            if (!langdata) return; // the record is gone (the session ended)
            let value = listValue(langdata.data);
            let is_silent = langdata.silent || false;
            console.log("data recieved from remote: ",langdata);
            // Set the language
            var cLang = $selectLang.val();
            if (value!==undefined && cLang !== value) {
                const customEvent = $.Event('change', {
                    detail: {
                        silent: is_silent
                    }
                });

                // a language that came from the session is not the visitor's own choice (32-remember-language.js keeps only that)
                window.__languageFromSession = true;
                try {
                    $selectLang.val(value).trigger(customEvent);
                } finally {
                    window.__languageFromSession = false;
                }
            }
        });

        // Hide the spinner
        $("#loader").fadeOut();
        $("#editor").fadeIn();

        // Initialize the ACE editor
        editor = ace.edit("editor");
        // a collaborator of a session that is over only reads (30-owner-presence.js)
        if (window.OwnerAway && window.OwnerAway.over()) editor.setReadOnly(true);
        try { editor.textInput.getElement().setAttribute("aria-label", "Code editor"); } catch (e) {} // T15
        editor.setTheme(getTheme(), function () { if (window.applyIdeTheme) window.applyIdeTheme(editor, getTheme()); });
        editor.setFontSize("14px");
        editor.$blockScrolling = Infinity;
        editor.setOptions({
            enableBasicAutocompletion: true,
            enableSnippets: true,
            enableLiveAutocompletion: true,
            fontFamily: CODE_FONT_STACK
        });
  
        // drag and drop feature
        editor.container.addEventListener("dragover", function(e) {
          e.preventDefault(); // prevent default behaviour given by browser
        });

        editor.container.addEventListener("drop", function(e) {
          e.preventDefault();
          var file = e.dataTransfer.files[0];
          var reader = new FileReader();
          reader.onload = function(e) {
            var contents = e.target.result;
            editor.setValue(contents);
          };
          reader.readAsText(file);
        });

        // key binding for file save
        editor.commands.addCommand({
            name: 'Save',
            bindKey: {win: 'Ctrl-S',  mac: 'Command-S'},
            exec: function(editor) {
              if (window.EditorTabs && window.EditorTabs.untitledShown && window.EditorTabs.untitledShown()) {
                window.EditorTabs.saveUntitled(); // an untitled tab is saved with a name (22-editor-tabs.js)
                return;
              }
              if ((editor.env.filename) && $('#file-browser').jstree(true).is_selected(editor.env.filename)) {
                SaveSelectedNodeToFile(editor.env.filename, function(){
                  // in case of failure refresh the tree to fetch from server
                  $('#file-browser').jstree(true).refresh();
                });
                console.log("file save triggered: ", editor.env.filename);
              } else {
                notify("Open a file from Files first, then press Ctrl+S to save it.", { type: "info" });
              }
            },
            readOnly: false, // false if this command should not apply in readOnly mode
        });

        // Get the queue reference
        var queueRef = currentEditorValue.child("queue");
        
        // This boolean is going to be true only when the value is being set programmatically
        // We don't want to end with an infinite cycle, since ACE editor triggers the
        // `change` event on programmatic changes (which, in fact, is a good thing)
        var applyingDeltas = false;

        // When we change something in the editor, update the value in Firebase
        editor.on("change", function(e) {
                    
            // In case the change is emitted by us, don't do anything
            // (see below, this boolean becomes `true` when we receive data from Firebase)
            if (applyingDeltas) {
                return;
            }
            // A collaborator's change is not shared while the owner is away (30-owner-presence.js): it stays in the page,
            // and cannot replace the owner's code when they come back.
            if (window.OwnerAway && window.OwnerAway.active()) {
                return;
            }

            // Set the content in the editor object
            // This is being used for new users, not for already-joined users.
            currentEditorValue.update({
                content: editor.getValue()
            });

            // Generate an id for the event in this format:
            //  <timestamp>:<random>
            // We use a random thingy just in case somebody is saving something EXACTLY
            // in the same moment
            queueRef.child(Date.now().toString() + ":" + Math.random().toString().slice(2)).set({
                event: e,
                by: uid
            }).catch(function(e) {
                console.error(e);
            });
        });

        // Get the editor document object 
        var doc = editor.getSession().getDocument();

        // Listen for updates in the queue
        queueRef.on("child_added", function (ref) {
        
            // Get the timestamp
            var timestamp = ref.key.split(":")[0];
        
            // Do not apply changes from the past
            if (openPageTimestamp > timestamp) {
                return;
            }
        
            // Get the snapshot value
            var value = ref.val();
            
            // In case it's me who changed the value, I am
            // not interested to see twice what I'm writing.
            // So, if the update is made by me, it doesn't
            // make sense to apply the update
            if (value.by === uid) { return; }
        
            // We're going to apply the changes by somebody else in our editor
            //  1. We turn applyingDeltas on
            applyingDeltas = true;
            //  2. Update the editor value with the event data
            doc.applyDeltas([value.event]);
            //  3. Turn off the applyingDeltas
            applyingDeltas = false;
        });
        
        // If the editor doesn't exist already....
        if (!initialcontent) {
            // ...we will initialize a new one. 
            // ...with this content:
            if (window[CONTENT_KEY]) {
              initialcontent = window[CONTENT_KEY];
            } else {
              initialcontent = "/* Welcome to openrepl! */";
            }

             //get language from optionmenu
            var optionlang = $('#optionlist option:selected').data('editor');
            if (optionlang==null) {
              optionlang="c"
            }
            // Here's where we set the initial content of the editor. Only the owner starts a session: a link to one
            // that has ended must not bring it back.
            if (typeof isMaster !== "function" || isMaster()) editorValues.child(editorId).set({
                lang: {
                  data: optionlang,
                  silent: true // trigger event only when told
                },
                queue: {},
                content: initialcontent
            });
        }

        // We're going to update the content, so let's turn on applyingDeltas 
        applyingDeltas = true;
        
        // ...then set the value
        // -1 will move the cursor at the begining of the editor, preventing
        // selecting all the code in the editor (which is happening by default)
        editor.setValue(initialcontent, -1);
        
        // ...then set applyingDeltas to false
        applyingDeltas = false;
        
        // And finally, focus the editor, unless that would scroll the page to it.
        var edRect = editor.container.getBoundingClientRect();
        if (edRect.top >= 0 && edRect.bottom <= window.innerHeight && !document.body.classList.contains("is-mobile")) {
          editor.focus();
        }
        // The language of the page is told to the session once the editor is ready. A collaborator's list still shows its
        // first entry then (Assembly), and telling that would replace the language of the owner on both pages: a
        // collaborator takes the language of the session, and tells its own only when the session has none yet.
        if (window.OwnerAway && window.OwnerAway.hostSession && (typeof isMaster !== "function" || isMaster())) {
          window.OwnerAway.hostSession(db, editorId); // the owner's page says it is here
        }
        if (typeof isMaster === "function" && !isMaster()) {
          currentEditorValue.child("lang").once("value", function (r) {
            var shared = r.val();
            if (!shared || shared.data === undefined) $("#select-lang").trigger('change');
          });
        } else {
          // the editor's mode follows the language of the page (the picker), not the first entry of its own list
          var pageMode = $('#optionlist option:selected').data('editor');
          // only while the list is still on its first entry: a file that was chosen before this (a picture, a PDF, a .py) has set it
          if (pageMode && $selectLang.val() === $selectLang.find('option:first').val() && $selectLang.val() !== pageMode && $selectLang.find('option[value="' + pageMode + '"]').length) $selectLang.val(pageMode);
          $("#select-lang").trigger('change');
        }
      }
    };  // end of editor init App

    // Take the editor value on start and set it in the editor
    currentEditorValue.child("content").once("value", function (contentRef) {
      // Get the current content
      var val = contentRef.val();
      if (window.OwnerAway && typeof isMaster === "function" && !isMaster()) {
        // a link that has ended or run out shows no code (30-owner-presence.js)
        window.OwnerAway.beforeInit(db, editorId, val, function (use) { initializeEditorApp(use); });
      } else {
        initializeEditorApp(val);
      }        
    });
    // initalize the editor App after 10s delay if above fails to
    setTimeout(initializeEditorApp, 10000);
});

export {};  // an ES module: strict mode, bundled by webpack
