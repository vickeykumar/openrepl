// utilities
var get = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelector(selector);
};

var getAll = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelectorAll(selector);
};

const CONTENT_KEY = "editorContent";
const  CMD_KEY = "command"
/*
function reloadCss()
{
    var links = document.getElementsByTagName("link");
    for (var cl in links)
    {
        var link = links[cl];
        if (link.rel === "stylesheet") {
            link.href += "";
            console.log("reloaded:%s",link.href);
        }
    }
}
*/
function ismob() {
   if(window.innerWidth <= 800 || window.innerHeight <= 480) {
     return true;
   } else {
     return false;
   }
}

function ToggleFunction() {
    TermElement = get(".terminal__row");
    xtermElement = get(".xterm",TermElement);
    TermElement.classList.toggle("fullscreen");
    if (xtermElement !== undefined && xtermElement !== null) {
      //console.log('xtermElement: ',xtermElement)
      xtermElement.classList.toggle("fullscreen");
    }
    var event = new Event('resize');
    window.dispatchEvent(event);
    togglebtn = get(".fa",TermElement);
    togglebtn.classList.toggle("fa-compress");
    togglebtn.classList.toggle("fa-expand");
    var AllElem = getAll("body > *");
    for (var i = 0; i < AllElem.length;i++)
    {
      var element = AllElem[i];
      if (element.id != "terminal__row" && element.tagName != "SCRIPT" ) {
        element.classList.toggle("hide-tag");
        console.log("class hidden for %s",element.tagName);
      }
    }
}

var einst = null;

// updates editor content by ID
function updateEditorContent(cmd="", content="/* Welcome to gorepl! */") {
    if(window[CMD_KEY]===cmd) {
      // no need to update as this is not an optionchange
      console.log("no change in command: ",cmd)
      return;
    }
    var editor = window["editor"];
    if( editor.env && editor.env.editor && editor.env.editor.getValue && (typeof(editor.env.editor.setValue) === "function")) {
        if (window.location.hash === "") {
          //master
          editor.env.editor.setValue(content);
        } else {
          //its a slave preserve the content
          content = editor.env.editor.getValue();
        }
    }
    window[CONTENT_KEY] = content; 
    window[CMD_KEY] = cmd;
}

function ToggleEditor() {
    TermElement = get("#terminal");
    ideElement =  get("#ide");
    editorbtn = get("#editor-button");
    if(editorbtn !==undefined && editorbtn !== null ) {
      editorbtn.classList.toggle("toggle-accent-color");
    }
    if (einst === null) {
      einst = Split(['#ide', '#terminal'], {
        gutterSize: 3,
        sizes: [55,45]
      });
      if (ideElement !==undefined && ideElement !== null) {
        ideElement.style.display = "block";
      }
    } else {
      einst.destroy();
      if (ideElement !==undefined && ideElement !== null) {
        ideElement.style.display = "none";
      }
      einst = null;
    }
}

function ToggleReconnect() {
    const optionMenu = get("#optionMenu");
    if(optionMenu!==undefined) {
        const option = get(".list", optionMenu);
        if (option!==undefined) {
          option.dispatchEvent(new Event("change"));
        }
    }
}

function CompileandRun() {
    const termElem = get("#terminal");
    if(termElem!==undefined) {
        termElem.dispatchEvent(new Event("optionrun"));
    }
}

function DownloadEditor() {
  var editor = window["editor"];
  if( editor.env && editor.env.editor && editor.env.editor.getValue && (typeof(editor.env.editor.setValue) === "function")) {
    content = editor.env.editor.getValue();
    filename = prompt("Please enter the filename to save");
    console.log(filename);
    if(filename) {
      var blob = new Blob([content], {type: "text/any;charset=utf-8"});
      saveAs(blob, filename);
    }
  }
}

(function myApp() {
  var speed = 250;
  var indx = 0;
  var i=0;
  var j=0;
  var cmd = '';
  var done = {};
  var classname = 'demo';
  var option2cmdMap = {
    "c":"cling",
    "cpp":"cling",
    "go":"gointerpreter",
    "java":"java",
    "python2.7":"python",
  };
  
  function displayWarningOnMobile () {
    // body... 
    var warningObj = get('.hero__warning');
    if (warningObj!==undefined) {
      if (ismob()) {
        warningObj.style.display = "block";
      } else if (warningObj.style.display !== "none") {
        warningObj.style.display = "none";
      }
    }
  }
  
  function getAbsoluteFrameUrl(url) {
	//replace all urls with with origin url in case of iframe webredirect
	var parent_origin = '';
	if (document.referrer !== '') {
	    	parent_origin = new URL(document.referrer).origin;
	}
	var absoluteUrl = new URL(url, window.location.href).href;
	if(window.self !== window.top && parent_origin !==window.location.origin) {
	    	//inside an iframe web redirect
		return absoluteUrl.replace(window.location.origin,parent_origin); 
	}
	return absoluteUrl;
  }

  function getOptEditorValue() {
    return $('#optionMenu > select option:selected').data('editor');
  }

  function getSelectValue() {
    // body...
    const optionMenu = get("#optionMenu");
    if(optionMenu!==undefined) {
        const option = get(".list", optionMenu);
        if (option!==undefined) {
          var cmd = option2cmdMap[option.value];
          if (cmd !== undefined) return cmd;
          return option.value;
        }
    }
    return '';
  }

  function typeItOut(classname, txt, _callback) {
    try {
      // statements
      //console.log('typing: '+txt+" index: "+indx + "callback: "+(typeof _callback));
      if (txt != undefined && indx < txt.length && !done[cmd]) {
        document.getElementsByClassName(classname)[0].innerHTML += txt.charAt(indx);
        indx++;
        setTimeout(function() {
          typeItOut(classname, txt, _callback);
        }, speed);
      } else {
        //console.log('done');
        if (typeof _callback === 'function' && !done[cmd]) {          
          _callback();  
        }
      }
    } catch(e) {
      // statements
      console.log(e);
      console.log('txt: ',txt);
    }
  }

  function PrintItOut(classname, txt, _callback) {
    document.getElementsByClassName(classname)[0].innerHTML += txt;
    if (typeof _callback === 'function' && !done[cmd]) {          
          _callback();  
    }
  }

  function PrintCode(code, _callback) {
    if (j < code.length && !done[cmd]) {
      indx = 0;
      //console.log('typing: ',code[j]["Statement"]);
      typeItOut(classname, code[j]["Statement"]+'\n', function(){
          PrintItOut(classname, code[j]['Result'] + '\n' + "<span class='code--prompt'>"+code[j]['Prompt']+"</span>");
          j++;
          setTimeout(PrintCode, 1800, code, _callback);
      });
    } else {
      if (typeof _callback === 'function' && !done[cmd]) {          
          _callback();  
      }
    }
  }

  function PrintDemo(codes, _callback) {
    try {
      // statements
      if (i < codes.length && !done[cmd]) {
        var code = codes[i]['Code'];
        document.getElementsByClassName(classname)[0].innerHTML = "<span class='code--prompt'>"+code[0]['Prompt']+"</span>";
        j=0;
        PrintCode(code,function(){
          i++;
          setTimeout(PrintDemo, 2800, codes, _callback);
        });
      } else {
        if (typeof _callback === 'function' && !done[cmd]) {          
            _callback();
            setTimeout(PrintDemo, 2800, codes, _callback);  
        }
      }
    } catch(e) {
      // statements
      console.log("Exception: ", e);
      console.log('codes: ', codes);
    }
  }

  function PrintUsage(obj,docm,cmd) {
    //docs
    var doc = get('.callout');
    var doclink = get('.button--primary',doc);
    if (cmd && doc &&  doclink) {
      if(docm==undefined|| docm=="") {
        docm = "./doc.html"
      }
      let absDocUrl = getAbsoluteFrameUrl(docm);
      doclink.setAttribute("href", absDocUrl);
    }
    var keybinding = get('.keybinding');
    var keybinding__details = getAll('.keybinding__detail',keybinding);
    //console.log('keybinding__details: ',keybinding__details);
    if (keybinding__details != undefined && keybinding__details.length >= 2) {
      var commands = getAll('li', keybinding__details[0]);
      var descriptions = getAll('li',keybinding__details[1]);
      for (var i = 0; i < commands.length; i++) {
        keybinding__details[0].removeChild(commands[i]);
        keybinding__details[1].removeChild(descriptions[i]);
      }
      if (obj != undefined) {
        for (var i = 0; i < obj.length; i++) {
          keybinding__details[0].innerHTML += '<li><span class="keybinding__label">'+obj[i]['Command']+'</span></li>';
          keybinding__details[1].innerHTML += '<li>'+obj[i]['Description']+'</li>';
        }
      }
    } else {
      console.log('Invalid keybinding__details');
    }
  }

  function PrintGithub(github_link) {
    var demo_term = get('.demo__terminal');
    var github = get('.github-corner',demo_term);
    if (github_link && github_link!=='' && github) {
      github.style.display = "block";
      github.setAttribute("href", github_link);
    } else {
      github.style.display = "none";
    }
  }

  function changeEditor() {
    var ide = get('#ide');
    var lang_selector = get('#select-lang', ide);
    var edname = getOptEditorValue();
    if (lang_selector.value !== edname) {
      console.log("editor: ",edname);
      lang_selector.value = edname;
      //notify editor to do the needfull
      lang_selector.dispatchEvent(new Event("change"));
    }
  }

  function actionOnchange() {
    setTimeout(changeEditor, 500);
    var http = new XMLHttpRequest();
    cmd = getSelectValue();
    console.log('command: ',cmd);
    var url = window.location.protocol + "//" + window.location.host + window.location.pathname + "demo?q=" + cmd;
    http.onreadystatechange = function() {
      if (this.readyState == 4 && this.status == 200) {
        try {
          var obj = JSON.parse(this.responseText);
          if ( obj!=undefined && obj["Status"]==="SUCCESS" && obj["Demo"]!=undefined ) {
            done[cmd] = false;
            setTimeout(PrintDemo, 1800, obj["Demo"]["Codes"],function() {
              // on completion of one cycle start another
              i=0;
            });
            setTimeout(PrintGithub, 200, obj["Demo"]["Github"]);
            setTimeout(updateEditorContent, 200, cmd, obj["Demo"]["Content"]);
            setTimeout(PrintUsage, 400, obj["Demo"]["Usage"],obj["Demo"]["Doc"],cmd);
          } else {
            console.log("Error: Unable to render Demo and Usage");
          }
        }
        catch(e) {
          // statements
          console.log("Exception: ",e);
          console.log(this.response);
        }
      }
    };
    http.open("GET", url, true);
    http.send();
  }

  const optionMenu = get("#optionMenu");
  if(optionMenu!==undefined) {
    const option = get(".list", optionMenu);
    if (option!==undefined) {
      //console.log('option: ',option);
      var _onchangefunction = option.onchange;
      option.addEventListener("change", function() {
        done[cmd] = true;
        setTimeout(actionOnchange,2000);
      });
    }
  }

  setTimeout(actionOnchange,1000);
  window.addEventListener("resize", displayWarningOnMobile);
  setTimeout(displayWarningOnMobile, 1000);

})();

/* setup typewriter effect in the Getting started demo
if (document.getElementsByClassName('demo').length > 0) {
  var i = 0;
  var txt = `scribbler
            [Entry mode; press Ctrl+D to save and quit; press Ctrl+C to quit without saving]

            ###todo for new year dinner party

            - milk
            - butter
            - green onion
            - lots and lots of kiwis 🥝`;
  var speed = 60;
  var cprompt = document.getElementsByClassName('demo')[0].innerHTML;
  function typeItOut () {
    if (i < txt.length) {
      document.getElementsByClassName('demo')[0].innerHTML += txt.charAt(i);
      i++;
      setTimeout(typeItOut, speed);
    } else {
      document.getElementsByClassName('demo')[0].innerHTML = cprompt;
      i=0;
      setTimeout(typeItOut, 3000);
    }
  }

  setTimeout(typeItOut, 1800);
}
*/

/* editor App */

$(function() {

    // Initialize Firebase configuration
    var config = {
        apiKey: "AIzaSyCcHshsJr7GXkyIaFOFRgfu76mS9eEUHOA",
        authDomain: "root-grammar-251415.firebaseapp.com",
        databaseURL: "https://root-grammar-251415.firebaseio.com"
    };
    firebase.initializeApp(config);

    const getExampleRef = () => {
      if(window.dbpath) {
        return window.dbpath;
      }
      var ref = firebase.database().ref();
      var hash = window.location.hash.replace(/#/g, '');
      if (hash) {
        window.dbpath = hash;
        return hash;
      }
      ref = ref.push();
      if (ref.key) {
        return ref.key;
      }
      return "xyz";
    };
    
    const changeOptionByData = (data="") => {
      var optionMenu = $('#optionMenu > select')[0];
      if (optionMenu) {
        console.log("changeOptionByData: ",data);
        for (var i = 0; i < optionMenu.length; i++){
          var option = optionMenu.options[i];
          if (option.getAttribute("data-editor") === data) {
            optionMenu.value = option.value;
            optionMenu.dispatchEvent(new Event("change"));
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

    // This function will return the user theme or the Monokai theme (which
    // is the default)
    function getTheme() {
        return localStorage.getItem(LS_THEME_KEY) || "ace/theme/monokai";
    }
    
    // Select the desired theme of the editor
    $("#select-theme").change(function () {
        // Set the theme in the editor
        editor.setTheme(this.value);
        
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
    
    // Select the desired programming language you want to code in 
    var $selectLang = $("#select-lang").change(function () {
        // Set the language in the Firebase object
        // This is a preference per editor
        currentEditorValue.update({
            lang: this.value
        });
        // Set the editor language
        if (editor) {
          editor.getSession().setMode("ace/mode/" + this.value);
        }

        //get language from optionmenu
        var optionlang = $('#optionMenu > select option:selected').data('editor');
        if ( optionlang && optionlang !== this.value ) {
          //local change triggered from editor
          //reflect in option menu
          changeOptionByData(this.value);
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

    // Take the editor value on start and set it in the editor
    currentEditorValue.child("content").once("value", function (contentRef) {

        // Somebody changed the lang. Hey, we have to update it in our editor too!
        currentEditorValue.child("lang").on("value", function (r) {
            var value = r.val();
            // Set the language
            var cLang = $selectLang.val();
            if (cLang !== value) {
                $selectLang.val(value).change();
            }
        });

        // Hide the spinner
        $("#loader").fadeOut();
        $("#editor").fadeIn();

        // Initialize the ACE editor
        editor = ace.edit("editor");
        editor.setTheme(getTheme());
        editor.$blockScrolling = Infinity;
        editor.setOptions({
            enableBasicAutocompletion: true,
            enableSnippets: true,
            enableLiveAutocompletion: true
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

        // Get the current content
        var val = contentRef.val();
        
        // If the editor doesn't exist already....
        if (val === null) {
            // ...we will initialize a new one. 
            // ...with this content:
            if (window[CONTENT_KEY]) {
              val = window[CONTENT_KEY];
            } else {
              val = "/* Welcome to gorepl! */\n/* Editor underdevelopment! */";
            }

            // Here's where we set the initial content of the editor
            editorValues.child(editorId).set({
                lang: "c_cpp",
                queue: {},
                content: val
            });
        }

        // We're going to update the content, so let's turn on applyingDeltas 
        applyingDeltas = true;
        
        // ...then set the value
        // -1 will move the cursor at the begining of the editor, preventing
        // selecting all the code in the editor (which is happening by default)
        editor.setValue(val, -1);
        
        // ...then set applyingDeltas to false
        applyingDeltas = false;
        
        // And finally, focus the editor!
        editor.focus();
        $("#select-lang").trigger('change');
    });
});
