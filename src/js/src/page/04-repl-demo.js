// The per-language demo in "Using the <language> REPL": typing animation, usage table and links, and language switching.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

(function myApp() {
  var speed = 250;
  var indx = 0;
  var i=0;
  var j=0;
  var cmd = '';
  var done = {};
  var classname = 'demo';
  
  function displayWarningOnMobile () {
    // The small-screen warning was replaced by the phone layout (T10).
    var warningObj = get('.hero__warning');
    if (warningObj) {
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
    return $('#optionlist option:selected').data('editor');
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
      var commands = getAll('li:not(.keybinding__title)', keybinding__details[0]);
      var descriptions = getAll('li:not(.keybinding__title)',keybinding__details[1]);
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
    var url = window.location.protocol + "//" + window.location.host + "/demo?q=" + cmd;
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
            if (!window.location.pathname.includes("practice")) {
              // don't override the questions
              setTimeout(updateEditorContent, 200, cmd, obj["Demo"]["Content"]);
              var starterLang = $("#optionlist").val();
              setTimeout(function () { if (typeof onStarterCodeLoaded === "function") onStarterCodeLoaded(starterLang); }, 450); // T13
            }
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

export {};  // an ES module: strict mode, bundled by webpack
