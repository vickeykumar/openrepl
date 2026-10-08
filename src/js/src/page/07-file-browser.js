// The Files panel: jstree, context menu, file operations and live file events.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

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

  // the types a folder has in the tree (see "types" below)
  const FOLDER_TYPES = ["default", "f-open", "f-closed"];

  // The type the server is told for a node: "file", or "folder" for a folder
  // whatever the tree calls it now. The server takes anything else for a file,
  // so an opened folder ("f-open") was removed and copied as if it were one,
  // which fails as soon as it has something in it.
  function wireType(type) {
    return type === "file" ? "file" : "folder";
  }

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
      'proto': 'protobuf',
      'tcl':  'tcl',
      'rs': 'rust',
      'sql': 'sql',
      'ts': 'typescript',
      'asm': 'assembly_x86'
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
        // A folder is "default" until it is opened or closed in the tree, which
        // makes it "f-open" or "f-closed" (for the icon). All three are folders:
        // each may hold the others, or an opened folder could not be moved, and
        // nothing could be moved into the home folder once it had been opened.
        "types": {
          "#": {
            "max_children": 1,
            //"max_depth": 4,
            "valid_children": ["root"]
          },
          "root": {
            "icon" : "fa fa-folder",
            "valid_children": FOLDER_TYPES.concat(["file"])
          },
          "default": {
            "icon" : "fa fa-folder",
            "valid_children": FOLDER_TYPES.concat(["file"])
          },
          "file": {
            "icon" : "fa fa-file",
            "valid_children": []
          },
          'f-open' : {
              'icon' : 'fa fa-folder-open',
              "valid_children": FOLDER_TYPES.concat(["file"])
          },
          'f-closed' : {
              'icon' : 'fa fa-folder',
              "valid_children": FOLDER_TYPES.concat(["file"])
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
            // jstree runs the item whose "shortcut" is the pressed key code while the menu is open
            // (Cmd/Ctrl+X works as well as a plain X); a string shortcut only shows the label
            var mod = /Mac|iP(hone|ad)/.test(navigator.platform) ? "\u2318" : "Ctrl+";
            var menuKey = function (code, letter, off) {
              return { "shortcut": off ? "off" : code, "shortcut_label": mod + letter };
            };
            var protectedNode = $globalitemnode.state.disabled ? true : false;
            var items = {
                "create": {
                  "label": "New",
                  "icon": "ctx-i ctx-i-plus",
                  "_disabled": ($globalitemnode.state.disabled || $globalitemnode.type=='file') ? true : false,
                  "submenu": {
                    "create_folder": {
                      "label": "Folder",
                      "icon": "ctx-i ctx-i-folder-plus",
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
                                  notify((xhr.responseText || error || "Please try again."), { type: "error", title: "Couldn't create the folder" });
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
                      "icon": "ctx-i ctx-i-file-plus",
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
                                  notify((xhr.responseText || error || "Please try again."), { type: "error", title: "Couldn't create the file" });
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
                  "icon": "ctx-i ctx-i-pencil",
                  "separator_after": true,
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
                            notify((xhr.responseText || error || "Please try again."), { type: "error", title: "Couldn't rename" });
                            ref.refresh();
                          });
                        }
                      }
                    });
                  }
                },
                "delete": {
                      "label": "Delete",
                      "icon": "ctx-i ctx-i-trash",
                      "_class": "ctx-danger",
                      "separator_before": true,
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
                            data: JSON.stringify({ Op: eventOp.Remove, Name: nodename, type: wireType(nodetype) }),
                            contentType: "application/json"
                          }).done(function(data) {
                            // Handle successful response
                            console.log("success Removing File: "+nodename);
                          }).fail(function(xhr, status, error) {
                            // Handle error
                            console.log("File Remove Failed ", status, error);
                            notify((xhr.responseText || error || "Please try again."), { type: "error", title: "Couldn't delete" });
                            ref.refresh();
                          });
                        }
                      }
                    },
                "cut": Object.assign({
                  "label": "Cut",
                  "icon": "ctx-i ctx-i-cut",
                  "_disabled": protectedNode,
                  "action": function (data) {
                    var ref = $.jstree.reference(data.reference);
                    ref.cut(data.reference);
                  }
                }, menuKey(88, "X", protectedNode)),
                "copy": Object.assign({
                  "label": "Copy",
                  "icon": "ctx-i ctx-i-copy",
                  "_disabled": protectedNode,
                  "action": function (data) {
                    var ref = $.jstree.reference(data.reference);
                    ref.copy(data.reference);
                  }
                }, menuKey(67, "C", protectedNode)),
                "paste": Object.assign({
                  "label": "Paste",
                  "icon": "ctx-i ctx-i-paste",
                  "separator_after": true,
                  "_disabled": protectedNode,
                  "action": function (data) {
                    var ref = $.jstree.reference(data.reference);
                    ref.paste(data.reference);
                  }
                }, menuKey(86, "V", protectedNode)),
                "save": Object.assign({
                  "label": "Save",
                  "icon": "ctx-i ctx-i-save",
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
                }, menuKey(83, "S", ($globalitemnode.state.disabled || $globalitemnode.type!=='file'))),
                "download": {
                  "label": "Download",
                  "icon": "ctx-i ctx-i-download",
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
            };
            // New only makes sense on a folder, and File is the common one
            if ($globalitemnode.type == 'file') {
              delete items.create;
            } else {
              var created = items.create.submenu;
              items.create.submenu = { "create_file": created.create_file, "create_folder": created.create_folder };
            }
            var menu = {};
            ["create", "rename", "cut", "copy", "paste", "save", "download", "delete"].forEach(function (name) {
              if (items[name]) { menu[name] = items[name]; }
            });
            return menu;
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
              data: JSON.stringify({ Op: op, Name: oldnameid, type: wireType(data.node.type), NewName: newid }),
              contentType: "application/json"
            }).done(function(data) {
              // Handle successful response
              console.log("File "+operation+" success"+oldnameid+" to "+newid);
            }).fail(function(xhr, status, error) {
              // Handle error
              console.log("File "+operation+" Failed ", status, error);
              notify((xhr.responseText || error || "Please try again."), { type: "error", title: "File " + operation + " failed" });
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
          
          // The file the editor already holds is not read from the disk again. The
          // tree is refreshed after a rename or a move (and its selection put back),
          // and reading the file again then threw away what was typed since the last
          // save: renaming any file wiped unsaved work in the open one.
          var alreadyOpen = window["editor"] && window["editor"].env && window["editor"].env.filename &&
            String(newSelectedNodeId) === String(window["editor"].env.filename);
          if (newSelectedNodeId!==undefined && newSelectedNodeId!="" && alreadyOpen) {
            applying_select = true;
            thisbrowser.update({
                selected_node: newSelectedNodeId
            });
          } else if (newSelectedNodeId!==undefined && newSelectedNodeId!="") {
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

  // ---- FileBrowser: the Files panel as an API for Genie's agent mode --------------------
  //
  // The same tree and the same requests as the context menu, with paths relative to
  // the home directory ("src/main.py"; "" is the home directory). Every call checks
  // the path against the tree first, so only what the server listed can be named;
  // and every call answers {ok, ...} instead of drawing a message. The agent mode
  // (chat-widget/src/agent.ts) is the only caller.
  (function () {
    var POST_WAIT_MS = 2500;   // how long to wait for the page's own request after a paste or a move
    var OPEN_WAIT_MS = 6000;   // how long a file may take to load into the editor
    var MAX_LISTED = 200;

    function tree() { return $('#file-browser').jstree(true); }
    function home() { return window.homedir || ""; }
    function idOf(rel) { return rel ? home() + "/" + rel : home(); }
    function relOf(id) { return id === home() ? "" : String(id).slice(home().length + 1); }
    function isFolder(node) { return node.type !== "file"; }
    function isHidden(node) { return String(node.text || "").startsWith("."); }
    function isProtected(node) { return !!(node.state && node.state.disabled); }
    function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
    function fail(error) { return { ok: false, error: error }; }

    function ready() {
      var t = tree();
      return !!t && !!home() && !!t.get_node(home());
    }

    // post sends one of the page's file requests (the body is the one the context menu sends)
    function post(body) {
      return new Promise(function (resolve) {
        $.ajax({
          url: preprocessurl("/ws_filebrowser"),
          method: "POST",
          data: JSON.stringify(body),
          contentType: "application/json"
        }).done(function () {
          resolve({ ok: true });
        }).fail(function (xhr, status, error) {
          resolve(fail(String(xhr.responseText || error || "the server refused it").trim().slice(0, 200)));
        });
      });
    }

    // For the operations the page performs itself in an event handler (paste, move):
    // collects what the server answered to its requests while they ran.
    function watchPosts() {
      var seen = 0;
      var errors = [];
      function onDone(e, xhr, settings) {
        var method = String(settings.type || settings.method || "").toUpperCase();
        if (method !== "POST" || String(settings.url).indexOf("/ws_filebrowser") < 0) return;
        seen++;
        if (xhr.status >= 400) errors.push(String(xhr.responseText || xhr.statusText || "the server refused it").trim().slice(0, 200));
      }
      $(document).on("ajaxComplete.genieFiles", onDone);
      return {
        finish: function () {
          return new Promise(function (resolve) {
            var waited = 0;
            (function poll() {
              if (seen > 0 || waited >= POST_WAIT_MS) {
                setTimeout(function () {
                  $(document).off("ajaxComplete.genieFiles", onDone);
                  resolve({ seen: seen, errors: errors });
                }, 150);
                return;
              }
              waited += 50;
              setTimeout(poll, 50);
            })();
          });
        }
      };
    }

    // a node that an operation may work on: it has to be in the tree, and not the home directory
    function target(rel, opts) {
      opts = opts || {};
      if (!ready()) return fail("the Files panel is not ready yet");
      var node = tree().get_node(idOf(rel));
      if (!node || (rel === "" && !opts.allowHome)) return fail((rel || "the home folder") + " is not in the Files panel");
      if (opts.folder && !isFolder(node)) return fail(rel + " is a file, not a folder");
      if (opts.file && isFolder(node)) return fail(rel + " is a folder, not a file");
      if (opts.writable && (isProtected(node) || isHidden(node))) return fail(rel + " is a protected file (hidden or binary): it is not Genie's to change");
      if (opts.notHidden && isHidden(node)) return fail(rel + " is a hidden file: it is not Genie's to change");
      return { ok: true, node: node };
    }

    // ---- the file the editor holds --------------------------------------------------
    // editor.env.filename is the path Run and Save use. When that file, or a folder
    // it is in, is renamed or moved, three things have to hold: what was typed is
    // saved first (the file is about to be found under another name), the editor is
    // told the new path, and the tree, which is refreshed, shows it selected again.

    function editorFile() {
      var e = window["editor"];
      return e && e.env ? String(e.env.filename || "") : "";
    }
    function setEditorFile(id) {
      var e = window["editor"];
      if (e && e.env) e.env.filename = id;
    }
    // id is the editor's file, or a folder that holds it
    function holdsEditorFile(id) {
      var f = editorFile();
      return !!f && (f === id || f.indexOf(id + "/") === 0);
    }
    // oldId is now newId: the editor's file follows it
    function retarget(oldId, newId) {
      var f = editorFile();
      if (f === oldId) setEditorFile(newId);
      else if (f && f.indexOf(oldId + "/") === 0) setEditorFile(newId + f.slice(oldId.length));
    }
    async function saveEditorFile() {
      var f = editorFile();
      var t = tree();
      if (!f || !t.get_node(f) || isFolder(t.get_node(f))) return { ok: true };
      var w = watchPosts();
      SaveSelectedNodeToFile(f);
      var done = await w.finish();
      if (done.errors.length) return fail("the open file could not be saved first: " + done.errors[0]);
      if (!done.seen) return fail("the open file could not be saved first: the server did not answer");
      return { ok: true };
    }

    // Reads the tree from the server again and waits for it: after a folder was
    // renamed, moved or copied, what is inside it has paths the tree does not know.
    function refreshTree() {
      return new Promise(function (resolve) {
        var over = false;
        var finish = function () {
          if (over) return;
          over = true;
          $('#file-browser').off("refresh.jstree.genieFiles");
          resolve();
        };
        $('#file-browser').on("refresh.jstree.genieFiles", function () { setTimeout(finish, 60); });
        setTimeout(finish, 4000);
        tree().refresh();
      });
    }

    // The editor's file is shown as the selected one, without reading it again.
    function showEditorFile() {
      var f = editorFile();
      var t = tree();
      if (!f || !t.get_node(f) || t.is_selected(f)) return;
      t.deselect_all(true);
      t.select_node(f, true);
    }

    function revealPanel() {
      // the Files panel starts folded away on a wide page; show it so that the user sees the work
      var panel = document.getElementById("files-panel");
      var toggle = document.getElementById("files-toggle");
      if (panel && toggle && panel.offsetParent === null) toggle.click();
    }

    function showNode(node) {
      var t = tree();
      try {
        var parents = (node.parents || []).filter(function (p) { return p !== "#"; });
        t.open_node(parents);
        var el = t.get_node(node, true);
        if (el && el.length && el[0].scrollIntoView) el[0].scrollIntoView({ block: "nearest" });
      } catch (e) {
        // showing it is not essential
      }
    }

    window.FileBrowser = {
      ready: ready,
      home: home,
      reveal: revealPanel,

      // what a path is: {exists, type: "file" | "folder", protected (binary, or hidden), hidden}
      info: function (rel) {
        if (!ready()) return { exists: false };
        var node = tree().get_node(idOf(rel));
        if (!node) return { exists: false };
        return { exists: true, type: isFolder(node) ? "folder" : "file", protected: isProtected(node), hidden: isHidden(node) };
      },

      // the file the editor is working on (its path), or ""
      current: function () {
        if (!ready()) return "";
        var sel = tree().get_selected();
        if (!sel.length) return "";
        var node = tree().get_node(sel[0]);
        return node && !isFolder(node) ? relOf(node.id) : "";
      },

      // "cut", "copy" or null: what a cut or a copy left in the panel
      buffer: function () {
        if (!ready()) return null;
        var b = tree().get_buffer();
        if (!b || !b.node || !b.node.length) return null;
        return b.mode === "move_node" ? "cut" : "copy";
      },

      list: function (rel) {
        var r = target(rel, { allowHome: true, folder: true });
        if (!r.ok) return r;
        revealPanel();
        var t = tree();
        var out = [];
        var more = false;
        (r.node.children_d || []).slice().sort().forEach(function (id) {
          var node = t.get_node(id);
          if (!node) return;
          var path = relOf(id);
          // hidden files and what is in a hidden folder are not listed
          if (path.split("/").some(function (seg) { return seg.charAt(0) === "."; })) return;
          if (out.length >= MAX_LISTED) { more = true; return; }
          out.push(path + (isFolder(node) ? "/" : "") + (isProtected(node) ? "  (not editable)" : ""));
        });
        return { ok: true, entries: out, truncated: more };
      },

      open: async function (rel) {
        var r = target(rel, { file: true });
        if (!r.ok) return r;
        if (isProtected(r.node) || isHidden(r.node)) return fail(rel + " cannot be opened in the editor (hidden or binary)");
        revealPanel();
        var t = tree();
        var editor = window["editor"];
        var filename = function () { return editor && editor.env ? editor.env.filename : ""; };
        if (t.is_selected(r.node) && filename() === r.node.id) return { ok: true, detail: "it was open already" };
        // what the editor holds belongs to the file that is open: save it first, as Run does
        var cur = t.get_selected();
        if (cur.length && t.get_node(cur[0]) && !isFolder(t.get_node(cur[0]))) {
          SaveSelectedNodeToFile(cur[0]);
          await sleep(400);
        }
        t.deselect_all(true);
        t.select_node(r.node.id);
        showNode(r.node);
        var waited = 0;
        while (filename() !== r.node.id && waited < OPEN_WAIT_MS) {
          await sleep(100);
          waited += 100;
        }
        if (filename() !== r.node.id) return fail(rel + " did not load into the editor");
        return { ok: true };
      },

      create: async function (rel, kind) {
        if (!ready()) return fail("the Files panel is not ready yet");
        var at = rel.lastIndexOf("/");
        var parentRel = at < 0 ? "" : rel.slice(0, at);
        var name = at < 0 ? rel : rel.slice(at + 1);
        var p = target(parentRel, { allowHome: true, folder: true });
        if (!p.ok) return fail("the folder " + (parentRel || "the home folder") + " is not in the Files panel");
        if (name.charAt(0) === ".") return fail("a name that starts with a dot is a hidden file: it is not Genie's to make");
        var t = tree();
        var id = idOf(rel);
        if (t.get_node(id)) return fail(rel + " exists already");
        revealPanel();
        var made = t.create_node(p.node.id, { id: id, text: name, type: kind === "folder" ? "default" : "file" }, "last");
        if (!made) return fail("the Files panel did not accept " + rel);
        if (kind !== "folder") t.set_icon(id, filename2IconClass(name));
        showNode(t.get_node(id));
        var res = await post({ Op: eventOp.Create, Name: id, type: kind === "folder" ? "folder" : "file" }); // wireType's two words
        if (!res.ok) {
          t.refresh();
          return res;
        }
        return { ok: true };
      },

      save: async function () {
        var cur = window.FileBrowser.current();
        if (!cur) return fail("no file is open: open one from the Files panel first");
        var w = watchPosts();
        SaveSelectedNodeToFile(idOf(cur));
        var done = await w.finish();
        if (done.errors.length) return fail(done.errors[0]);
        if (!done.seen) return fail("the server did not answer");
        return { ok: true, detail: "saved " + cur };
      },

      rename: async function (rel, newName) {
        var r = target(rel, { writable: true });
        if (!r.ok) return r;
        if (newName.charAt(0) === ".") return fail("a name that starts with a dot is a hidden file: it is not Genie's to make");
        var t = tree();
        var node = r.node;
        var oldid = node.id;
        var newid = node.parent + "/" + newName;
        var folder = isFolder(node);
        if (t.get_node(newid)) return fail(newName + " exists already in that folder");
        var mine = holdsEditorFile(oldid);
        if (mine) {
          var saved = await saveEditorFile();
          if (!saved.ok) return saved;
        }
        revealPanel();
        if (!t.rename_node(node, newName)) return fail("the Files panel did not accept the name " + newName);
        if (!t.set_id(node.id, newid)) {
          t.refresh();
          return fail("the Files panel did not accept the name " + newName);
        }
        var res = await post({ Op: eventOp.Rename, Name: oldid, type: wireType(node.type), NewName: newid });
        if (!res.ok) {
          await refreshTree();
          return res;
        }
        retarget(oldid, newid);
        if (folder || mine) {
          await refreshTree();
          showEditorFile();
        }
        return { ok: true };
      },

      remove: async function (rel) {
        var r = target(rel, { notHidden: true });
        if (!r.ok) return r;
        var t = tree();
        var node = r.node;
        var id = node.id;
        var type = wireType(node.type);
        var mine = holdsEditorFile(id);
        revealPanel();
        if (!t.delete_node(node)) return fail("the Files panel did not accept it");
        var res = await post({ Op: eventOp.Remove, Name: id, type: type });
        if (!res.ok) {
          await refreshTree();
          return res;
        }
        // the editor must not go on writing to a file that is gone
        if (mine) setEditorFile("");
        return { ok: true, detail: mine ? "the file that was open in the editor is gone with it; the editor still shows its text, which is in no file now" : undefined };
      },

      cut: function (rel) {
        var r = target(rel, { writable: true });
        if (!r.ok) return r;
        revealPanel();
        tree().cut(r.node);
        showNode(r.node);
        return { ok: true };
      },

      copy: function (rel) {
        var r = target(rel, { writable: true });
        if (!r.ok) return r;
        revealPanel();
        tree().copy(r.node);
        showNode(r.node);
        return { ok: true };
      },

      paste: async function (toRel) {
        var dest = target(toRel, { allowHome: true, folder: true });
        if (!dest.ok) return dest;
        var t = tree();
        var buf = t.get_buffer();
        if (!buf || !buf.node || !buf.node.length) return fail("nothing is cut or copied: use files_cut or files_copy first");
        var cut = buf.mode === "move_node";
        var moved = buf.node.map(function (n) { return { from: n.id, to: dest.node.id + "/" + n.text }; });
        var mine = cut && moved.some(function (m) { return holdsEditorFile(m.from); });
        if (mine) {
          var saved = await saveEditorFile();
          if (!saved.ok) return saved;
        }
        revealPanel();
        var w = watchPosts();
        t.paste(dest.node);
        var done = await w.finish();
        if (done.errors.length) {
          await refreshTree();
          return fail(done.errors[0]);
        }
        if (!done.seen) return fail("nothing was pasted (the same folder, or a name that is taken there)");
        if (cut) moved.forEach(function (m) { retarget(m.from, m.to); });
        // what is inside a folder that was moved or copied has new paths
        await refreshTree();
        showEditorFile();
        var shown = tree().get_node(dest.node.id);
        if (shown) showNode(shown);
        return { ok: true };
      },

      move: async function (rel, toRel) {
        var r = target(rel, { writable: true });
        if (!r.ok) return r;
        var dest = target(toRel, { allowHome: true, folder: true });
        if (!dest.ok) return dest;
        if (r.node.parent === dest.node.id) return fail(rel + " is in that folder already");
        if (dest.node.id === r.node.id || (dest.node.parents || []).indexOf(r.node.id) >= 0) return fail("a folder cannot be moved into itself");
        if (tree().get_node(dest.node.id + "/" + r.node.text)) return fail("that folder has " + r.node.text + " already");
        var oldid = r.node.id;
        var newid = dest.node.id + "/" + r.node.text;
        var mine = holdsEditorFile(oldid);
        if (mine) {
          var saved = await saveEditorFile();
          if (!saved.ok) return saved;
        }
        revealPanel();
        var w = watchPosts();
        var ok = tree().move_node(r.node, dest.node);
        var done = await w.finish();
        if (!ok || !done.seen) {
          await refreshTree();
          return fail("the Files panel did not accept the move");
        }
        if (done.errors.length) {
          await refreshTree();
          return fail(done.errors[0]);
        }
        retarget(oldid, newid);
        await refreshTree();
        showEditorFile();
        var shown = tree().get_node(dest.node.id);
        if (shown) showNode(shown);
        return { ok: true };
      }
    };
  })();

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

export {};  // an ES module: strict mode, bundled by webpack
