// utilities
var get = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelector(selector);
};

var getAll = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelectorAll(selector);
};

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

function ToggleFunction() {
    TermElement = get(".terminal");
    xtermElement = get(".xterm",TermElement);
    TermElement.classList.toggle("fullscreen");
    if (xtermElement !== undefined && xtermElement !== null) {
      console.log('xtermElement: ',xtermElement)
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
      if (element.id != "terminal" && element.tagName != "SCRIPT") {
        element.classList.toggle("hide-tag");
        console.log("class hidden for %s",element.tagName);
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
  function getSelectValue() {
    // body...
    const optionMenu = get("#optionMenu");
    if(optionMenu!==undefined) {
        const option = get(".list", optionMenu);
        if (option!==undefined) {
          return option2cmdMap[option.value];
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

  function PrintUsage(obj,cmd) {
    //docs
    var doc = get('.callout');
    var doclink = get('.button--primary',doc);
    if (cmd && doc &&  doclink) {
      doclink.setAttribute("href", "./docs/"+cmd+".html");
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

  function actionOnchange() {
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
            setTimeout(PrintUsage, 400, obj["Demo"]["Usage"],cmd);
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
