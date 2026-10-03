// Home directory, the Firebase share path, device and master checks, language options and the jid helpers.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.handleClickOutside = handleClickOutside;
window.ismob = ismob;
window.isMaster = isMaster;
window.getSelectValue = getSelectValue;
window.getjidstr = getjidstr;
window.preprocessurl = preprocessurl;

window.homedir = ""; // home directory

window.getExampleRef = () => {
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

window.option2cmdMap = {
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
    return 'c';
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

export {};  // an ES module: strict mode, bundled by webpack
