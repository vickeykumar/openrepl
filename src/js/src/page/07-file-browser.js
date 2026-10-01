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
                            data: JSON.stringify({ Op: eventOp.Remove, Name: nodename, type: nodetype }),
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
              data: JSON.stringify({ Op: op, Name: oldnameid, type: data.node.type, NewName: newid }),
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

export {};  // an ES module: strict mode, bundled by webpack
