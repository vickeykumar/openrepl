// utilities
// Notes: usage "string"==["string"] (loose) vs "string"===["string"] (strict)
var get = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelector(selector);
};

var getAll = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelectorAll(selector);
};

const CONTENT_KEY = "editorContent";
const CMD_KEY = "command";
const MAX_FILESIZE = 20 * 1024 * 1024;
const HOME_DIR_KEY = "homedir";
var homedir = ""; // home directory

const getExampleRef = () => {
  if(window.dbpath) {
    return window.dbpath;
  }
  var ref = firebase.database().ref("openrepl");
  var hash = window.location.hash.replace(/#/g, '');
  if (hash) {
    window.dbpath = hash;
    return hash;
  }
  ref = ref.push();
  if (ref.key) {
    window.dbpath = ref.key;  // save the new reference key for future reference
    return ref.key;
  }
  return "xyz";
};


function handleClickOutside(elementselector, callback) {
  $(document).on('click', function(event) {
    var element = $(elementselector);
    if (!element.length) return; // If the element is not found, exit the function

    var target = $(event.target);

    // Check if the clicked target is the element or its descendants
    if (element.is(target) || element.has(target).length > 0) {
      return; // Clicked inside the element or its children, do nothing
    }

    // Get the bounding rectangle of the element
    var rect = element[0].getBoundingClientRect();
    
    // Check if the click is within the bounding rectangle of the element
    var clickInside = (
      event.clientX >= rect.left &&
      event.clientX <= rect.right &&
      event.clientY >= rect.top &&
      event.clientY <= rect.bottom
    );

    if (!clickInside) {
      callback(); // Take the specified action
    }
  });
}


function ismob() {
   if(window.innerWidth <= 800 || window.innerHeight <= 480) {
     return true;
   } else {
     return false;
   }
}

function isMaster() {
  var hash = window.location.hash.replace(/#/g, '');
  if (!hash) {
      return true;
  } else {
      return false;
  }
}

var option2cmdMap = {
    "c":"cling",
    "cpp":"cling",
    "go":"gointerpreter",
    "java":"java",
    "python2.7":"python",
};

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

function getjidstr() {
  const locationurl = new URL(window.location.href);
  const jidstr = locationurl.searchParams.get('jid')!==null ? locationurl.searchParams.get('jid') : "";
  return jidstr;
}

// adds the jidstring to url for further processing
function preprocessurl(url) {
    const jidstr = getjidstr();
    // update jidstr in url
    if (jidstr !== "") {
      // check url has already query strings and update.
      if (url.indexOf("?") === -1) {
        url = url + "?jid="+jidstr;
      } else {
        url = url + "&jid="+jidstr;
      }
    }

    // update homedir for slaves
    if (!isMaster() && (homedir)) {
      if (url.indexOf("?") === -1) {
        url = url + "?"+HOME_DIR_KEY+"="+homedir;
      } else {
        url = url + "&"+HOME_DIR_KEY+"="+homedir;
      }
    }
    return url;
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
    togglebtn = get("#togglescreen-button>.fa",TermElement);
    togglebtn.classList.toggle("fa-compress");
    togglebtn.classList.toggle("fa-expand");
    var AllElem = getAll("body > *");
    for (var i = 0; i < AllElem.length;i++)
    {
      var element = AllElem[i];
      if (element.id != "terminal__row" && element.tagName != "SCRIPT" && !element.classList.contains("ace_editor")) {
        element.classList.toggle("hide-tag");
        //console.log("class hidden for %s",element.tagName);
      }
    }

    // toggle tooltiptext and tooltip in fullscreen-toggle
    AllElem = getAll(".fullscreen-toggle", TermElement);
    for (var i = 0; i < AllElem.length;i++)
    {
      var element = AllElem[i];
      element.classList.toggle("tooltip");
      var tiptextelem = get(".tooltiptext",element);
      tiptextelem.classList.toggle("hide-tag");
    }
}

var einst = null;

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
          //master
          editor.env.editor.setValue(content);
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
        alert("Failed to save file: "+oldSelectedNodeId +" : " + error + " : "+xhr.responseText);
        if (typeof(errcallback)==="function") {
          errcallback();
        }
      });
    }
}

function ToggleEditor() {
    TermElement = get("#terminal-div");
    ideElement =  get("#ide");
    editorbtn = get("#editor-button");
    if(editorbtn !==undefined && editorbtn !== null ) {
      editorbtn.classList.toggle("toggle-accent-color");
    }
    if (einst === null) {
      let default_right = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--gutter-right')) || 0;
      let default_prevMouseX = 0;
      einst = Split(['#ide', '#terminal-div'], {
        gutterSize: 3,
        sizes: [55,45],
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

function LoadOptionFromUrl() {
	var searchParams = new URL(location.href.toLowerCase()).searchParams;
	var repl = searchParams.get("repl");
    var optionMenu = $('#optionMenu > select')[0];
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
  introJs().setOptions({
    //disableInteraction: true,
    steps: [{
      title: 'Welcome aboard !! 👋',
      intro: 'New to OpenREPL? Lets take a Tour on how to use OpenREPL.'
    },
    {
      element: document.querySelector('.hero__title'),
      intro: 'OpenREPL is an Open Source Platform that lets you run and test code snippets in an interactive shell (like python).'
    },
    {
      element: document.querySelector('#optionlist'),
      intro: 'you can choose one of the many available Programming languages options for creating an REPL.'
    },
    {
      element: document.querySelector('#terminal__row'),
      intro: 'This is the Terminal window you will use to interact with OpenREPL'
    },
    {
      title: 'IDE Window',
      element: document.querySelector('#ide'),
      intro: 'you can write code snippets here and run the same.'
    },
    {
      title: 'IDE Menu bar',
      element: document.querySelector('#controls-buttons'),
      intro: 'IDE Menu bar to control ide features.'
    },
    {
      title: 'Terminal Window',
      element: document.querySelector('#terminal-div'),
      intro: 'This is the Terminal window you will use to interact with OpenREPL interactively or see the ide code results when you run.'
    },
    {
      title: 'REPL Menu bar',
      element: document.querySelector('#sidebar_options'),
      intro: 'you can choose one of the many options from this menu bar to interact with OpenREPL and perform a task. \
      Available Features: Maximize/Minimize, show/hide IDE, Reconnect REPL, Run IDE Code, Debug IDE code, \
      Download IDE code, Upload IDE code Respectively.'
    },
    {
      title: 'Demo Getting Started',
      element: document.querySelector('.demo__terminal'),
      intro: 'Refer sample demo on REPL usage here for quickly getting started.'
    },
    {
      title: 'Github Link',
      element: document.querySelector('#demo_github-corner'),
      intro: 'github links for respective opensource repl.'
    },
    {
      title: 'REPL Usage Commands',
      element: document.querySelector('#repl_usage'),
      intro: 'Check out some REPL usage commands.'
    },
    {
      title: 'Documentation',
      element: document.querySelector('#callout_doc'),
      intro: 'Documentation on current REPL used.'
    },
    {
      title: 'Fork this REPL',
      element: document.querySelector('#fork-widget'),
      intro: 'Fork this REPL/Terminal to share same containers and codepath/home\
       (Both REPLs can communicate with same network interface).'
    },
    {
      title: 'Share/Collaborate',
      element: document.querySelector('#share-btn-1'),
      intro: 'Copy this url to Share REPL and Collaborate with others.'
    },
    {
      title: 'query repl',
      element: document.querySelector('#home_menu'),
      intro: 'you can jump to an particular repl directly using query repl by name \
      followed by ? in url (like https://openrepl.com/?python.'
    },
    {
      title: 'Github-Star',
      intro: 'If you like my work Please take you valuable time to leave a Github-star. \
      it means a lot and will encouage us to introduce more exciting features.'
    },
    {
      title: "And",
      element: document.querySelector('#share-buttons'),
      intro: 'Share with you friends.'
    },
    {
      title: 'Good Luck With Your Learning Journey!',
      element: document.querySelector('#three'),
      intro: 'And Leave a feedback what you want to see next!'
    }]
  }).start();
}

function CompileandRun() {
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
    content = editor.env.editor.getValue();
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
    alert('File size exceeds the limit of 10MB');
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
          alert("File uploaded successfully");
          // reset
          document.getElementById("fileToLoad").value="";
          $("#loader").fadeOut();
        },
        error: function(xhr, status, error) {
          alert("Failed to upload File: "+error+" : "+xhr.responseText);
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

(function myApp() {
  var speed = 250;
  var indx = 0;
  var i=0;
  var j=0;
  var cmd = '';
  var done = {};
  var classname = 'demo';
  
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

function logout () {
      var xhr = new XMLHttpRequest();
      var url = window.location.protocol + "//" + window.location.host + window.location.pathname + "logout";
      xhr.open("POST", url, true);
      xhr.setRequestHeader("Content-Type", "application/json");
      xhr.onreadystatechange = function () {
      if (xhr.readyState === 4 && xhr.status === 200) {
        console.log("logout success");
      }
      console.log("logout status: ",xhr.status);
      // logout anyway
      location.assign("/");
      };
      xhr.send();
}

function renderProfileData () {
      var xhr = new XMLHttpRequest();
      var url = window.location.protocol + "//" + window.location.host + window.location.pathname + "profile?q=json";
      xhr.open("GET", url, true);
      xhr.setRequestHeader("Content-Type", "application/json");
      xhr.onreadystatechange = function () {
        if (xhr.readyState === 4 && xhr.status === 200) {
          try {
            var user = JSON.parse(this.responseText);
            if ( user!=undefined ) {
              console.log("response: ", user);
              if ( user.photoURL !== undefined && user.photoURL !=="" ) {
                document.getElementById('user-image').src=user.photoURL;
              }

            } else {
              console.log("undefined response");
            }
          }
          catch(e) {
            // statements
            console.log("Exception: ",e);
            console.log(this.response);
          }
        }
        console.log("login status: ",xhr.status);
      };
      xhr.send();
}

/* Sign in App */

$(function() {

    // Initialize Firebase App
    if (firebase.apps.length === 0) {
        firebase.initializeApp(firebaseconfig);
    }
    var ui = new firebaseui.auth.AuthUI(firebase.auth());
    var uiConfig = {
      callbacks: {
        signInSuccessWithAuthResult: function(authResult, redirectUrl) {
          // User successfully signed in.
          // Return type determines whether we continue the redirect automatically
          // or whether we leave that to developer to handle.
          console.log("authResult: ",JSON.stringify(authResult), JSON.stringify(redirectUrl));

	  if ((authResult.user) && (authResult.user.emailVerified)) {
              // User is signed in and email is verified, so proceed with login
              xhr = new XMLHttpRequest();
              var url = window.location.protocol + "//" + window.location.host + window.location.pathname + "login";
              xhr.open("POST", url, true);
              xhr.setRequestHeader("Content-Type", "application/json");
              xhr.onreadystatechange = function () {
                  if (xhr.readyState === 4) {
                      if (xhr.status === 200) {
                          console.log("login success");
                      } else {
                          alert("Login Failed.");
                      }
                      // redirect anyway
                      location.reload();
                  }
                  console.log("login status: ",xhr.status);
              };
              xhr.send(JSON.stringify(authResult));
          } else {
              // User's email is not verified
              console.log("email not verified.");

              var message = "Please check your inbox and follow the instructions to verify your email address and LogIn again. If you haven't received the verification email, you can click the button below to send again.";

              // Create a header element
              var header = document.createElement("h2");
              header.innerText = "A verification email has been sent to your inbox";

              if ((authResult.additionalUserInfo) && (!authResult.additionalUserInfo.isNewUser)) {
                message = "Your email address has not been verified yet. "+message;
                header.innerText = "Email not verified";
              }
             
              if ((authResult.additionalUserInfo) && (authResult.additionalUserInfo.isNewUser)) {
	      // new user send email
                firebase.auth().currentUser.sendEmailVerification()
                      .then(function() {
                          console.log("Verification email sent");
                      })
                      .catch(function(error) {
                          console.log(error);
                      });
              }

              // Create a message element
              var messageElement = document.createElement("h3");
              messageElement.innerText = message;

              // Create a button element
              var button = document.createElement("button");
              button.innerText = "Send again";
              button.classList.add("share-btn");
              button.onclick = function() {
                  firebase.auth().currentUser.sendEmailVerification()
                      .then(function() {
                          console.log("Verification email sent");
                          alert("A verification email has been sent to your inbox. Please follow the instructions to verify your email address.");
                      })
                      .catch(function(error) {
                          console.log(error);
                      });
              };

              // Add the elements to the page
              var container = document.getElementById("firebaseui-auth-container");
              container.innerHTML = "";
              container.appendChild(header);
              container.appendChild(messageElement);
              container.appendChild(button);
          }

          return false;
        },
      },
      // Will use popup for IDP Providers sign-in flow instead of the default, redirect.
      signInFlow: 'popup',
      signInSuccessUrl: '/',
      signInOptions: [
        // Leave the lines as is for the providers you want to offer your users.
        firebase.auth.EmailAuthProvider.PROVIDER_ID,
        firebase.auth.GoogleAuthProvider.PROVIDER_ID,
        //firebase.auth.FacebookAuthProvider.PROVIDER_ID,
        //firebase.auth.TwitterAuthProvider.PROVIDER_ID,
        //firebase.auth.GithubAuthProvider.PROVIDER_ID,
        //firebase.auth.PhoneAuthProvider.PROVIDER_ID
      ],
      // Terms of service url.
      tosUrl: '/about.html',
      // Privacy policy url.
      privacyPolicyUrl: '/about.html'
    };


    // all the auth handling is done in client side to improve performance(cache)
      // in stead of golang templates
      // install sign in button
    var signin_btn = document.getElementById('sign-in-button');
    var signout_btn = document.getElementById('sign-out-button');

    function startauth() {
          // remove sign in button
          signin_btn.style.display="none";
          /*firebaseuiElem = get('#firebaseui-auth-container');
          firebaseuiElem.classList.toggle("fullscreen");*/
          var AllElem = getAll("body > *");
          for (var i = 0; i < AllElem.length;i++)
          {
            var element = AllElem[i];

            if (element.id == "footer") {
                element.classList.toggle("fixed-footer");
            }

            if (element.id != "header-nav" && element.id != "footer" && element.id != "firebaseui-auth-container" && element.tagName != "SCRIPT") {
              element.classList.toggle("hide-tag");
              console.log("class hidden for %s",element.tagName);
            }
          }
          ui.start('#firebaseui-auth-container', uiConfig);
      }


    // check if already logged in 
    xhr = new XMLHttpRequest();
    var url = window.location.protocol + "//" + window.location.host + window.location.pathname + "login";
    xhr.open("GET", url, true);
    xhr.setRequestHeader("Content-Type", "application/json");
    xhr.onreadystatechange = function () {
      if (xhr.readyState === 4 && xhr.status === 200) {
        try {
          var user = JSON.parse(this.responseText);
          if ( user!=undefined ) {
            console.log("response: ", user);
          } else {
            console.log("undefined response");
          }

          //install new one
          if (user.loggedIn) {
            // install sign out button
            if (signin_btn !==null) {
                //remove previous icon
                signin_btn.style.display="none";
                document.getElementById('user-account').style.display="inline-block";

                // render other profile data, take ur time
                setTimeout(renderProfileData, 2800);
            }

          }
        }
        catch(e) {
          // statements
          console.log("Exception: ",e);
          console.log(this.response);
        }
      }
      console.log("login status: ",xhr.status);
    };
    xhr.send();

    // install the auth handler
    if (signin_btn !==null) {
      signin_btn.addEventListener('click', startauth);
    }

    if (signout_btn !== null && signout_btn !== undefined) {
      // install signout from firebase as well
      signout_btn.addEventListener('click', function() {
        firebase.auth().signOut();
      });
    }
});   


/* editor App */

$(function() {

    // Initialize Firebase App
    if (firebase.apps.length === 0) {
      firebase.initializeApp(firebaseconfig);
    }
    
    const changeOptionByData = (data="", is_silent=false) => {
      var optionMenu = $('#optionMenu > select')[0];
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
    var $selectLang = $("#select-lang").change(function (event) {
        // Check if the silent event
        let is_silent = (event.detail && event.detail.silent) || false;
        console.log("is silent change: ", is_silent, event);
        // Set the language in the Firebase object
        // This is a preference per editor
        currentEditorValue.update({
            lang: {
              data: this.value,
              silent: is_silent // trigger event only when told
            }
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
    const initializeEditorApp = (initalcontent="/* Welcome to openrepl! */") => {
      if (!editorInitialized) {
        //all init for editor goes here
        editorInitialized = true;
        // Somebody changed the lang. Hey, we have to update it in our editor too!
        currentEditorValue.child("lang").on("value", function (r) {
            let langdata = r.val();
            let value = langdata.data;
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

                $selectLang.val(value).trigger(customEvent);
            }
        });

        // Hide the spinner
        $("#loader").fadeOut();
        $("#editor").fadeIn();

        // Initialize the ACE editor
        editor = ace.edit("editor");
        editor.setTheme(getTheme());
        editor.setFontSize("14px");
        editor.$blockScrolling = Infinity;
        editor.setOptions({
            enableBasicAutocompletion: true,
            enableSnippets: true,
            enableLiveAutocompletion: true
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
              if ((editor.env.filename) && $('#file-browser').jstree(true).is_selected(editor.env.filename)) {
                SaveSelectedNodeToFile(editor.env.filename, function(){
                  // in case of failure refresh the tree to fetch from server
                  $('#file-browser').jstree(true).refresh();
                });
                console.log("file save triggered: ", editor.env.filename);
              } else {
                alert("No File Selected To Save.");
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
        if (initalcontent === null) {
            // ...we will initialize a new one. 
            // ...with this content:
            if (window[CONTENT_KEY]) {
              initalcontent = window[CONTENT_KEY];
            } else {
              initalcontent = "/* Welcome to openrepl! */\n/* Editor underdevelopment! */";
            }

             //get language from optionmenu
            var optionlang = $('#optionMenu > select option:selected').data('editor');
            if (optionlang==null) {
              optionlang="c_cpp"
            }
            // Here's where we set the initial content of the editor
            editorValues.child(editorId).set({
                lang: {
                  data: optionlang,
                  silent: true // trigger event only when told
                },
                queue: {},
                content: initalcontent
            });
        }

        // We're going to update the content, so let's turn on applyingDeltas 
        applyingDeltas = true;
        
        // ...then set the value
        // -1 will move the cursor at the begining of the editor, preventing
        // selecting all the code in the editor (which is happening by default)
        editor.setValue(initalcontent, -1);
        
        // ...then set applyingDeltas to false
        applyingDeltas = false;
        
        // And finally, focus the editor!
        editor.focus();
        $("#select-lang").trigger('change');
      }
    };  // end of editor init App

    // Take the editor value on start and set it in the editor
    currentEditorValue.child("content").once("value", function (contentRef) {
      // Get the current content
      var val = contentRef.val();
      initializeEditorApp(val);        
    });
    // initalize the editor App after 10s delay if above fails to
    setTimeout(initializeEditorApp, 10000);
});


/* file-browser App */

(function() {
  const eventOp = {
    Create: 1 << 0,
    Write: 1 << 1,
    Remove: 1 << 2,
    Rename: 1 << 3,
    Chmod: 1 << 4,
  }

  const disabled_ops = ["move_node"];   // disabled operations for restricted and hidden files

  var lastreciever = "";  // last nodeid that recieved a write event
  var writecounter = 0;  // number of updates recievd by the selected node
  const MIN_WRITES = 10;  // minimum number of write events before we fetch the data again from server
  const STATE_TTL = 900;  // TTL to save the state of selected node

    const codeext2menuoption = {
      'js': 'javascript',
      'html': 'html',
      'css': 'css',
      'py': 'python',
      'rb': 'ruby',
      'java': 'java',
      'c': 'c_cpp',
      'cpp': 'c_cpp',
      'go': 'golang',
      'pl': 'perl',
      'sh': 'sh',
      'ksh': 'sh',
      'bash': 'sh',
      'json': 'json',
      'text': 'text',
      'txt': 'text',
      'xml': 'xml',
      'toml': 'toml',
      'yaml': 'yaml',
      'proto': 'protobuf'
  };
  const imageext = ['jpg', 'jpeg', 'gif', 'png', 'bmp', 'webp', 'svg', 'ico'];
  const archiveext = ['zip', 'rar', '7z', 'tar', 'gz', 'bz2'];
  const audioext = ['mp3', 'wav', 'wma', 'aac', 'flac', 'm4a', 'ogg', 'opus'];
  const videoext = ['mp4', 'webm', 'ogg', 'avi', 'wmv', 'flv', 'mov', 'mkv'];
  const objectext = ['out', 'o', 'exe', 'bin', 'so', 'dll'];
  const otherdocs = ["doc","docx","ppt","pptx","pdf"];
  const ext2icon = {
    "txt"   : "fa fa-file-text",
    "pdf"   : "fa fa-file-pdf",
    "ppt"   : "fa fa-file-powerpoint",
    "pptx"  : "fa fa-file-powerpoint",
    "doc"   : "fa fa-file-word",
    "docx"   : "fa fa-file-word",
  }

  function isCodeFile(filename) {
      var ext = filename.split('.').pop().toLowerCase();
      return ext in codeext2menuoption;
  }

  function isObjFile(filename) {
      var ext = filename.split('.').pop().toLowerCase();
      return objectext.includes(ext);
  }

  function isVideoFile(filename) {
      var ext = filename.split('.').pop().toLowerCase();
      return videoext.includes(ext);
  }

  function isImageFile(filename) {
    var ext = filename.split('.').pop().toLowerCase();
    return imageext.includes(ext);
  }

  function isArchiveFile(filename) {
    var ext = filename.split('.').pop().toLowerCase();
    return archiveext.includes(ext);
  }

  function isAudioFile(filename) {
    var ext = filename.split('.').pop().toLowerCase();
    return audioext.includes(ext);
  }

  function isOtherDocFile(filename) {
    var ext = filename.split('.').pop().toLowerCase();
    return otherdocs.includes(ext);
  }

  function filename2IconClass(filename) {
    if (isCodeFile(filename)) { 
      return "fa fa-file-code"; 
    }
    if (isObjFile(filename)) { 
      return "fa fa-gear"; 
    }
    if (isImageFile(filename)) { 
      return "fa fa-file-image"; 
    }
    if (isArchiveFile(filename)) { 
      return "fa fa-file-archive"; 
    }
    if (isVideoFile(filename)) { 
      return "fa fa-file-video"; 
    }
    if (isAudioFile(filename)) { 
      return "fa fa-file-audio"; 
    }

    var ext = filename.split('.').pop().toLowerCase();
    if (ext2icon.hasOwnProperty(ext)) {
        return ext2icon[ext];
    } else {
        return "fa fa-file";
    }
    return "fa fa-file";
  }

  function shoulddisable(filename) {
    // should disable the hidden files, usually startes with . or any other binary file format that can't be loaded to the editor
    return (filename.startsWith('.') || isObjFile(filename) || isVideoFile(filename) || isAudioFile(filename) || 
      isArchiveFile(filename) || isImageFile(filename) || isOtherDocFile(filename));
  }


  // Initialize Firebase App
  if (firebase.apps.length === 0) {
    firebase.initializeApp(firebaseconfig);
  }
  // get reference to current browser
  var browserId = getExampleRef();
  // write if not present already
  var browserslist = firebase.database().ref("file-browser"); 
  // Get the current browser reference
  var thisbrowser = browserslist.child(browserId);


  function preprocessnodedata(node) {
    var disabled = false;
    // Check if data.type is file
    if (node.type === 'file') {
      // Add icon class to data
      node.icon = filename2IconClass(node.text);
      disabled = shoulddisable(node.text);
    } else {
      // Recursively preprocess children of folder
      if (Array.isArray(node.children)) {
        node.children = node.children.map(function(child) {
          return preprocessnodedata(child);
        });
      }
    }
    // disable the hidden files, usually startes with . or any other binary file format that can't be loaded to the editor
    if ((node.text) && (node.text.startsWith('.') || disabled)) {
      console.log("disabled node: ", node);
      node.state = { 'disabled': true };
      node.draggable = false;
    }
    return node;
  }

  function IsNodeSelected(nodeid) {
    // code to get the selected node
    var tree = $('#file-browser').jstree(true);
    var sel = tree.get_selected();
    if (!sel.length) { return false; }
    return (nodeid === sel[0]);
  }

  function LoadSelectedNodeFromFile(newSelectedNodeId, errcallback=null) {
    // load the new selected file
        let type =  $('#file-browser').jstree(true).get_node(newSelectedNodeId).type;
        if (type === "file") {
          $.ajax({
            url: preprocessurl("/ws_filebrowser?q=load&filepath="+newSelectedNodeId),
            method: "GET"
          }).done(function(data) {
            // Handle successful response
            var decodedResponse = atob(data);
            updateEditorContent("", decodedResponse, true);
            console.log("File Loaded successfully: "+newSelectedNodeId);
          }).fail(function(xhr, status, error) {
            // Handle error
            console.log("Failed to load file: "+newSelectedNodeId, status, error);
            if (typeof(errcallback)==="function") {
              errcallback();
            }
          });
        } else {
          // folder type, clear filename
          let editor = window["editor"];
          if (editor && editor.env) {
            editor.env.filename = "";
            console.log("resetting filename for foldertype.", newSelectedNodeId);
          }
        }
  }

  var lang_change_scheduled = false;
  function changelangbyselectednode() {
    var ide = get('#ide');
    var lang_selector = get('#select-lang', ide);
    var filename = "";
    var filetype = "";
    // code to get the selected node
    var tree = $('#file-browser').jstree(true);
    if (tree) {
      var sel = tree.get_selected();
      if (sel.length > 0) { 
        filename = sel[0];
        filetype = tree.get_type(sel);
      }
    }
    var ext = filename.split('.').pop().toLowerCase();
    if (filetype == "file" && ext in codeext2menuoption) {
      if (lang_selector.value !== codeext2menuoption[ext]) {
        console.log("editor language detected: ",codeext2menuoption[ext]);
        lang_selector.value = codeext2menuoption[ext];
        //notify editor to do the needfull

        if (!lang_change_scheduled) {
          lang_change_scheduled = true;
          setTimeout(function() {
            const customEvent = new CustomEvent('change', {
                detail: { silent: true }
            });
            lang_selector.dispatchEvent(customEvent);
            // change the menu option without firing the reconnect of new language
            // will catch up if user reconnects it
            lang_change_scheduled = false;
          }, 1000); // schedule a change in editor lang if applicable
        }

      }
    }
  }

  // eventhandler to process events recieved by server
    function eventhandler (eventdata) {
      console.log("Event data recieved by jstree-browser: ", eventdata);
      switch(eventdata.Op) {
        case eventOp.Create:
          console.log("Create recieved for: ", eventdata.Name);
          let parentnode = eventdata.Name.split('/').slice(0, -1).join('/');
          let nodename = eventdata.Name.split('/').slice(-1).join('/');
          $('#file-browser').jstree(true).create_node(parentnode, preprocessnodedata({ "id" : eventdata.Name, "text" : nodename, "type": eventdata.type }), "last", function(){
              console.log("node created: ", eventdata.Name);
           });
          break;
        case eventOp.Write:
          console.log("Write recieved for: ", eventdata.Name);
          if (IsNodeSelected(eventdata.Name)) {
            if (lastreciever!==eventdata.Name) {
              lastreciever = eventdata.Name;  // update the recievername
              writecounter = 0;    // reinit the counter again
            } else {
              writecounter++;  // update the write counter
            }

            if (writecounter >= MIN_WRITES) {
              // its time to load the file again from server
              LoadSelectedNodeFromFile(eventdata.Name);
              writecounter = 0;    // reinit the counter again, to wait for next threshold writes
            }
          }
          break;
        case eventOp.Remove:
          console.log("Remove recieved for: ", eventdata.Name);
          $('#file-browser').jstree(true).delete_node(eventdata.Name, function() {
              console.log("Node with id " + eventdata.Name + " is deleted.");
          });
          break;
        case eventOp.Rename:
          console.log("Rename recieved for: ", eventdata.Name);
          // no way to track as of now, so refresh
          $('#file-browser').jstree(true).refresh();
          break;
        case eventOp.Chmod:
          console.log("Chmod recieved for: ", eventdata.Name);
          break;
        default:
          console.log("Invalid eventdata recieved: ", eventdata);
          break;
      }

    }

  // Take the homedir value on start and set it in the browser
  thisbrowser.child("content").once("value", function (contentRef) {
      var applying_select = false;
      // set the homedir in the begining
      var nodedata = contentRef.val();
      if ((nodedata) && (nodedata.id)) {
        homedir = nodedata.id;
      }

      $('#file-browser').jstree({
        "core": {
          "animation": 200,
          "check_callback": function(operation, node, parent, position, more) {
            if (node.text.startsWith('.') && disabled_ops.includes(operation)) {
              // Disable dnd and other ops for hiddend node and restricted nodes
                return false;
            }
            // Allow other operations and nodes to have normal dnd behavior
            return true;
          },
          "themes": {
            "stripes": true
          },
          "data": {
            'url': function () {
                var cmd = getSelectValue();
                var url = preprocessurl('/ws_filebrowser'+'?command='+cmd);
                console.log("requesting url: ", url);
                return url;
              },
            'dataType': 'json',
             "dataFilter" : function (data) {
                var node = preprocessnodedata(JSON.parse(data));
                // master updates homedir and content so that slave can consume
                if (isMaster() && (node.id)) {
                  homedir=node.id;
                  thisbrowser.update({
                      content: node
                  });
                  console.log("node saved : ",node);
                }
                return JSON.stringify(node);
             }
          },
          "drawCallback": function() {
            // your code here
            $('#file-browser>ul').prepend('<div class="main-menu-bar" id="main-menu-bar"><i class="fa fa-files-o"></i><i class="fa fa-close" ></i></div>');
          },
        },
        "types": {
          "#": {
            "max_children": 1,
            //"max_depth": 4,
            "valid_children": ["root"]
          },
          "root": {
            "icon" : "fa fa-folder",
            "valid_children": ["default", "file"]
          },
          "default": {
            "icon" : "fa fa-folder",
            "valid_children": ["default", "file"]
          },
          "file": {
            "icon" : "fa fa-file",
            "valid_children": []
          },
          'f-open' : {
              'icon' : 'fa fa-folder-open'
          },
          'f-closed' : {
              'icon' : 'fa fa-folder'
          },
        },

        "plugins": [
          "ajax", "contextmenu", "dnd", "search",
           "types", "wholerow", "unique", "changed", "state"
        ],
        "unique": {
          "case_sensitive": true
        },
        'state': {
            'ttl': STATE_TTL
        },

        "contextmenu": {
          show_at_node: true,
          select_node: true,
          "items": function ($globalitemnode) {
            console.log("globalitemnode: ", $globalitemnode);
            return {
                "create": {
                  "label": "New",
                  "_disabled": ($globalitemnode.state.disabled || $globalitemnode.type=='file') ? true : false,
                  "submenu": {
                    "create_folder": {
                      "label": "Folder",
                      "action": function (data) {
                        var ref = $.jstree.reference(data.reference);
                        var sel = ref.get_selected();
                        if(!sel.length) { return false; }
                        sel = sel[0];
                        sel = ref.create_node(sel, {"type": "default"});
                        if(sel) {
                          ref.edit(sel, null, function(node, status, cancelled) {
                            if (!cancelled) {
                              console.log("node: ",node);
                              // calculate new node id
                              var newid = node.parent+"/"+node.text;
                              if (ref.set_id(node.id, newid)) {
                                $.ajax({
                                  url: preprocessurl("/ws_filebrowser"),
                                  method: "POST",
                                  data: JSON.stringify({ Op: eventOp.Create, Name: newid, type: "folder" }),
                                  contentType: "application/json"
                                }).done(function(data) {
                                  // Handle successful response
                                  console.log("success creating folder: ", newid);
                                  setTimeout(function() {
                                      $('#file-browser').jstree(true).deselect_all();
                                      $('#file-browser').jstree(true).select_node(newid);
                                  }, 100);
                                }).fail(function(xhr, status, error) {
                                  // Handle error
                                  console.log("Folder Create Failed ", status, error);
                                  alert("Folder Create Failed: "+ error + " : "+xhr.responseText);
                                  ref.refresh();
                                });
                              }
                            }
                          });
                        }
                      }
                    },
                    "create_file": {
                      "label": "File",
                      "action": function (data) {
                        var ref = $.jstree.reference(data.reference);
                        var sel = ref.get_selected();
                        if(!sel.length) { return false; }
                        sel = sel[0];
                        sel = ref.create_node(sel, {"type": "file"});
                        if(sel) {
                          ref.edit(sel, null, function(node, status, cancelled) {
                            if (!cancelled) {
                              // calculate new node id
                              var newid = node.parent+"/"+node.text;
                              if (ref.set_id(node.id, newid)) {
                                $.ajax({
                                  url: preprocessurl("/ws_filebrowser"),
                                  method: "POST",
                                  data: JSON.stringify({ Op: eventOp.Create, Name: newid, type: "file" }),
                                  contentType: "application/json"
                                }).done(function(data) {
                                  // Handle successful response
                                  console.log("success creating file: ", newid);
                                  ref.set_icon(newid, filename2IconClass(newid));
                                  setTimeout(function() {
                                      $('#file-browser').jstree(true).deselect_all();
                                      $('#file-browser').jstree(true).select_node(newid);
                                  }, 100);
                                }).fail(function(xhr, status, error) {
                                  // Handle error
                                  console.log("File Create Failed ", status, error);
                                  alert("File Create Failed: "+error+ " : "+xhr.responseText);
                                  ref.refresh();
                                });
                              }
                            }
                          });
                        }
                      }
                    }
                  }
                },
                "rename": {
                  "label": "Rename",
                  "_disabled": $globalitemnode.state.disabled ? true : false,
                  "action": function (data) {
                    var ref = $.jstree.reference(data.reference);
                    var sel = ref.get_selected();
                    if(!sel.length) { return false; }
                    var nodename = sel[0];
                    ref.edit(sel, null, function(node, status, cancelled) {
                      if (!cancelled) {
                        // calculate new node id
                        var oldid = node.id;
                        var newid = node.parent+"/"+node.text;
                        if (ref.set_id(node.id, newid)) {
                          $.ajax({
                            url: preprocessurl("/ws_filebrowser"),
                            method: "POST",
                            data: JSON.stringify({ Op: eventOp.Rename, Name: oldid, type: node.type, NewName: newid }),
                            contentType: "application/json"
                          }).done(function(data) {
                            // Handle successful response
                            console.log("success Renaming File "+oldid+" to "+newid);
                          }).fail(function(xhr, status, error) {
                            // Handle error
                            console.log("File Rename Failed ", status, error);
                            alert("File Rename Failed: "+error+ " : "+xhr.responseText);
                            ref.refresh();
                          });
                        }
                      }
                    });
                  }
                },
                "delete": {
                      "label": "Delete",
			// disable delete, why if user needs to cleanup
                      "action": function (data) {
                        var ref = $.jstree.reference(data.reference);
                        var sel = ref.get_selected();
                        if(!sel.length) { sel.push($globalitemnode.id); }	//assign for which this operation is triggered
                        var nodename = sel[0];
                        var nodetype = ref.get_type(sel);
                        if (ref.delete_node(sel)) {
                          $.ajax({
                            url: preprocessurl("/ws_filebrowser"),
                            method: "POST",
                            data: JSON.stringify({ Op: eventOp.Remove, Name: nodename, type: nodetype }),
                            contentType: "application/json"
                          }).done(function(data) {
                            // Handle successful response
                            console.log("success Removing File: "+nodename);
                          }).fail(function(xhr, status, error) {
                            // Handle error
                            console.log("File Remove Failed ", status, error);
                            alert("File Remove Failed: " + error+ " : "+xhr.responseText);
                            ref.refresh();
                          });
                        }
                      }
                    },
                "edit": {
                  "label": "Edit",
                  "_disabled": $globalitemnode.state.disabled ? true : false,
                  "submenu": {
                    "cut": {
                      "label": "Cut",
                      "action": function (data) {
                        var ref = $.jstree.reference(data.reference);
                        ref.cut(data.reference);
                      }
                    },
                    "copy": {
                      "label": "Copy",
                      "action": function (data) {
                        var ref = $.jstree.reference(data.reference);
                        ref.copy(data.reference);
                      }
                    },
                    "paste": {
                      "label": "Paste",
                      "action": function (data) {
                        var ref = $.jstree.reference(data.reference);
                        ref.paste(data.reference);
                      }
                    },
                  }
                },
                "save": {
                  "label": "Save",
                  "_disabled": ($globalitemnode.state.disabled || $globalitemnode.type!=='file') ? true : false,
                  "action": function (data) {
                    var ref = $.jstree.reference(data.reference);
                    var sel = ref.get_selected();
                    if(!sel.length) { return false; }
                    var nodename = sel[0];
                    SaveSelectedNodeToFile(nodename, function(){
                      // in case of failure refresh the tree to fetch server side tree
                      $('#file-browser').jstree(true).refresh();
                    });
                  }
                },
                "download": {
                  "label": "Download",
                  "_disabled": ($globalitemnode.state.disabled && $globalitemnode.text.startsWith('.')) ? true : false,
                  "action": function (data) {
                    var ref = $.jstree.reference(data.reference);
                    var sel = ref.get_selected();
	            if(!sel.length) { sel.push($globalitemnode.id); }
                    var nodename = sel[0];
                    var nodetype = ref.get_type(sel);
                    var link = document.createElement("a");
                    link.href = preprocessurl("/ws_filebrowser?q=zip&filepath="+nodename);
                    link.click();
                  }
                }
              }
            }
      },

      }).on('ready.jstree', function() {
          // Add custom row after jstree has finished rendering
          $('#file-browser>ul').prepend('<div class="main-menu-bar" id="main-menu-bar"><i class="fa fa-files-o"></i><i class="fa fa-close" ></i></div>');

          // refresh the tree when an change or run is triggered on terminal to get uptodate homedir
          $("#terminal.active").on('optionchange optionrun', function(event) {
            if (!isMaster()) {
              // no need of redundant saves
              return;
            }
            // save the editor content to the selected file before we run anything
            var tree = $('#file-browser').jstree(true);
            var sel = tree.get_selected();
            if (sel.length > 0) { 
              var nodename = sel[0];
              var nodetype = tree.get_type(sel);
              if (nodetype=="file") {
                SaveSelectedNodeToFile(nodename);
              }
            } 
            // refresh on optionchange, doing that in option run can be costly so skip
            if (event.type=="optionchange") {
              var selected_node = $('#file-browser').jstree(true).get_selected();
              $('#file-browser').jstree(true).refresh();
              if (selected_node.length > 0) {
                $('#file-browser').jstree(true).select_node(selected_node, true); 
              }
              // select back the previous node after refresh, no dup events
              console.log('tree refreshed on: ', event.type);
            }
          });

          $(document).ready(function() {
            // set local eventhandler for using gotty to communicate with server, when doc is ready
            gotty.setEventHandler(eventhandler);
          });

        }).on('refresh.jstree', function() {
                // reinitialize all the shared stuffs
                $('#file-browser>ul').prepend('<div class="main-menu-bar" id="main-menu-bar"><i class="fa fa-files-o"></i><i class="fa fa-close" ></i></div>');
                applying_select = false;
                lang_change_scheduled = false;
          // call your custom function here
        }).on("move_node.jstree copy_node.jstree", function (e, data) {
          var op = eventOp.Rename;  // move
          if (e.type === "copy_node") {
            op = eventOp.Create;
          }
          var operation = (op === eventOp.Rename ? "Move" : "Copy");
          console.log("recieved data: ", operation, data, data.node);
          var oldParentId = data.old_parent;
          var newParentId = data.parent;
          var movedNodeId = data.node.id;
          var nodename = data.node.text;
          var newid = newParentId +"/"+nodename;
          console.log(operation+" node with ID " + movedNodeId + " name: " +nodename+ " from " + oldParentId + " to " + newParentId);
          var ref = $.jstree.reference(movedNodeId);
          if (ref.set_id(movedNodeId, newid)) {
            var oldnameid = oldParentId+"/"+nodename;
            //if (oldParentId == newParentId) { return; }
            $.ajax({
              url: preprocessurl("/ws_filebrowser"),
              method: "POST",
              data: JSON.stringify({ Op: op, Name: oldnameid, type: data.node.type, NewName: newid }),
              contentType: "application/json"
            }).done(function(data) {
              // Handle successful response
              console.log("File "+operation+" success"+oldnameid+" to "+newid);
            }).fail(function(xhr, status, error) {
              // Handle error
              console.log("File "+operation+" Failed ", status, error);
              alert("File "+operation+" Failed: " + error + " : "+xhr.responseText);
              ref.refresh();
            });
          }
        }).on("changed.jstree", function(e, data) {
          console.log(" changed event data: ",data, data.node);
          if (data.action !== "select_node") { return; } // no need to do anything for any other event
          // get the old selected node ID
          var oldSelectedNodeId = data.changed.deselected;
          // get the new selected node ID
          var newSelectedNodeId = data.changed.selected;

          // do something with the old and new node IDs
          console.log("Deselected node ID: " + oldSelectedNodeId);
          console.log("New selected node ID: " + newSelectedNodeId);

	  /* As of now save on deselection not supported, so make sure to save before leaving or deselecting
          if (oldSelectedNodeId!==undefined && oldSelectedNodeId!="") {
              SaveSelectedNodeToFile(oldSelectedNodeId, function(){
                // in case of failure deselect all to avoid confusion and refresh the tree
                $('#file-browser').jstree(true).deselect_all(true);
                $('#file-browser').jstree(true).refresh();
              });
          }*/
          
          if (newSelectedNodeId!==undefined && newSelectedNodeId!="") {
              LoadSelectedNodeFromFile(newSelectedNodeId, function(){
                // in case of failure deselect all to avoid confusion and refresh the tree
                $('#file-browser').jstree(true).deselect_all(true);
                $('#file-browser').jstree(true).refresh();
              });

            applying_select = true;
            thisbrowser.update({
                selected_node: newSelectedNodeId
            });
          }
          
        }).on('open_node.jstree', function (e, data) {
            data.instance.set_type(data.node,'f-open');
        }).on('close_node.jstree', function (e, data) {
            data.instance.set_type(data.node.id,'f-closed');
        });

        // sync selected nodes accross all shares
        thisbrowser.child("selected_node").on("value", function (snapshot) {
            const nodeid = snapshot.val();
            if (applying_select) {
                // this seleect is triggered by me only, return
                console.log("selection triggered by me: ", (isMaster()?"master":"slave"), nodeid);
                applying_select = false;
                changelangbyselectednode(); 
                //check if we can update the new language for new selection
                return;
            }
            if (nodeid) {
              console.log("new node selected: ", nodeid);
              // deselect the node and select node with node id
              $('#file-browser').jstree(true).deselect_all();
              $('#file-browser').jstree(true).select_node(nodeid);
            }
            applying_select = false;
        });
        // master changed content. refresh the tree to update the content
        thisbrowser.child("content").on("value", function (snapshot) {
          if (!isMaster()) {
            // slaves set the content and refresh
            nodedata = snapshot.val();
            if ((nodedata) && (nodedata.id)) {
              homedir = nodedata.id;
              $('#file-browser').jstree(true).settings.core.data = nodedata;
              $('#file-browser').jstree(true).refresh();
            }
          }
        });

    });


    // when context menu is shown
  $(document).bind('context_show.vakata', function (reference, element, position) {
      $('.main-menu').addClass('expanded');
  });

  $(document).ready(function() {
    $('.main-menu').on('click focusin', function() {
       console.log('menu focused');
      $(this).addClass('expanded');
    }).on('focusout', function() {
       console.log('menu blurred');
      $(this).removeClass('expanded');
    });
    setTimeout(function() {
      gotty.launcher(firebaseconfig); // launch gotty term
    }, 1000);

    handleClickOutside("#file-browser", function() {
      if ($('#file-browser').hasClass('expanded')) {
        $('#file-browser').removeClass('expanded');
      }
    });
  });

})();
