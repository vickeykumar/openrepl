package livepreview

// bridgeCmds is the second script of the bridge. The first one (BridgeScript, rewrite.go) tells the pane what the page does; this
// one lets the pane (and through it Genie's agent) look at the page and act in it: read it as numbered parts, click, type, press a
// key, scroll, select, hover, wait, inspect a part, run an expression, and use the page's storage. A command is
// {openreplCmd: "click", id: 7, ref: "e12", ...}; the answer is {openrepl: "reply", id: 7, result: {...}}. A command is taken only
// from the page that holds the frame (e.source === parent): the page's own code cannot pose as it. It also reports the page's
// popups and downloads.
//
// No template literals and no backquote in it: it is a Go raw string.
const bridgeCmds = `<script>(function(){var M="openrepl",R={},N=0,lines=[],pos=0,ring=null,ringT=0,C={};
function post(m){try{m[M]=m.t;parent.postMessage(m,"*")}catch(e){}}
function reply(id,r){post({t:"reply",id:id,result:r})}
function q(s){return JSON.stringify(String(s))}
function sh(s,n){s=String(s==null?"":s);return s.length>n?s.slice(0,n)+"...":s}
var SKIP={SCRIPT:1,STYLE:1,NOSCRIPT:1,TEMPLATE:1,HEAD:1,META:1,LINK:1,TITLE:1,SVG:0};
var BLOCK={P:1,DIV:1,LI:1,UL:1,OL:1,TABLE:1,TR:1,TD:1,TH:1,SECTION:1,ARTICLE:1,HEADER:1,FOOTER:1,NAV:1,MAIN:1,ASIDE:1,FORM:1,BLOCKQUOTE:1,PRE:1,FIGURE:1,FIGCAPTION:1,DL:1,DT:1,DD:1,BR:1,HR:1,FIELDSET:1,DETAILS:1,SUMMARY:1,CAPTION:1,H1:1,H2:1,H3:1,H4:1,H5:1,H6:1};
function vis(e){if(e.hidden)return false;var s;try{s=getComputedStyle(e)}catch(x){return true}return !(s.display==="none"||s.visibility==="hidden"||s.visibility==="collapse")}
function inView(e){var r=e.getBoundingClientRect();return r.bottom>0&&r.top<innerHeight&&r.right>0&&r.left<innerWidth}
function roleOf(e){return(e.getAttribute("role")||"").toLowerCase()}
function inter(e){var t=e.tagName;if(t==="CANVAS"){var cr=e.getBoundingClientRect();return cr.width>=40&&cr.height>=40}if(t==="A")return e.hasAttribute("href");if(t==="BUTTON"||t==="SELECT"||t==="TEXTAREA"||t==="SUMMARY")return true;if(t==="INPUT")return e.type!=="hidden";var r=roleOf(e);if(r&&/^(button|link|checkbox|radio|tab|menuitem|switch|option|textbox|combobox|slider)$/.test(r))return true;var c=e.getAttribute("contenteditable");if(c!==null&&c!=="false")return true;return e.hasAttribute("onclick")}
function nm(e){var v=e.getAttribute("aria-label");if(v)return v;var ids=e.getAttribute("aria-labelledby");if(ids){var s=ids.split(/\s+/).map(function(i){var x=document.getElementById(i);return x?x.textContent:""}).join(" ").replace(/\s+/g," ").trim();if(s)return s}
if(e.labels&&e.labels.length){var l=(e.labels[0].textContent||"").replace(/\s+/g," ").trim();if(l)return l}
var t=e.tagName==="INPUT"||e.tagName==="SELECT"||e.tagName==="TEXTAREA"?"":(e.textContent||"").replace(/\s+/g," ").trim();if(t)return t;
var im=e.querySelector&&e.querySelector("img[alt]");if(im&&im.alt)return im.alt;
var ty=(e.type||"").toLowerCase();return e.getAttribute("title")||e.getAttribute("placeholder")||(e.tagName==="INPUT"&&(ty==="submit"||ty==="button"||ty==="reset")?e.value:"")||e.getAttribute("name")||e.id||""}
function part(e){var id="e"+(++N);R[id]=e;var sg0=sigOf(e);SIG[id]={s:sg0,i:(SC[sg0]=(SC[sg0]||0)+1)-1};var t=e.tagName,ty=(e.type||"").toLowerCase(),role=roleOf(e),k,x="";
if(t==="A"||role==="link"){k="link";x=" -> "+sh(e.getAttribute("href")||"",80)}
else if(t==="SELECT"){k="select";var o=e.options[e.selectedIndex];x=" value="+q(o?o.text:"")+" options="+q([].slice.call(e.options,0,12).map(function(z){return z.text}).join("|"))}
else if(ty==="checkbox"||ty==="radio"||role==="checkbox"||role==="radio"||role==="switch"){k=ty==="radio"||role==="radio"?"radio":"checkbox";x=(e.checked||e.getAttribute("aria-checked")==="true")?" checked":" unchecked"}
else if(t==="TEXTAREA"||(t==="INPUT"&&ty!=="submit"&&ty!=="button"&&ty!=="reset"&&ty!=="image")||role==="textbox"||role==="combobox"||(e.getAttribute("contenteditable")!==null&&e.getAttribute("contenteditable")!=="false")){k="field";x=" type="+(ty||"text")+(ty==="password"?"":" value="+q(sh(e.value!==undefined?e.value:e.textContent,80)))}
else if(t==="CANVAS"){var cb=e.getBoundingClientRect();k="canvas";x=" "+Math.round(cb.width)+"x"+Math.round(cb.height)+(e.width&&(e.width!==Math.round(cb.width)||e.height!==Math.round(cb.height))?" drawing "+e.width+"x"+e.height:"")}
else if(t==="BUTTON"||t==="SUMMARY"||ty==="submit"||ty==="button"||ty==="reset"||ty==="image"||role==="button"||role==="tab"||role==="menuitem"){k=role==="tab"?"tab":"button"}
else k=role||"element";
return id+" "+k+" "+q(sh(nm(e),80))+x+(e.disabled?" disabled":"")+(BX&&t!=="CANVAS"?boxOf(e):"")}
var BX=false;
function boxOf(e){var r=e.getBoundingClientRect();return" @"+Math.round(r.left+scrollX)+","+Math.round(r.top+scrollY)+" "+Math.round(r.width)+"x"+Math.round(r.height)}
function build(vp){lines=[];R={};SIG={};SC={};N=0;pos=0;var txt="";
function flush(){var t=txt.replace(/\s+/g," ").trim();txt="";if(t)lines.push(sh(t,300))}
function walk(n){if(lines.length>3000)return;
if(n.nodeType===3){if(!vp||(n.parentElement&&inView(n.parentElement)))txt+=n.nodeValue;return}
if(n.nodeType!==1||SKIP[n.tagName]===1||/^openrepl-/.test(n.id))return;
if(!vis(n))return;
if(inter(n)){flush();if(!vp||inView(n))lines.push(part(n));return}
var hm=/^H([1-6])$/.exec(n.tagName);
if(hm){flush();[].forEach.call(n.childNodes,walk);var h=txt.replace(/\s+/g," ").trim();txt="";if(h)lines.push("h"+hm[1]+" "+sh(h,200));return}
var b=BLOCK[n.tagName]===1;if(b)flush();
if(n.tagName==="IMG"){var al=n.getAttribute("alt");if(al)txt+=" [image: "+al+"] "}
[].forEach.call(n.childNodes,walk);if(b)flush()}
if(document.body)walk(document.body);flush()}
function scrollInfo(){var d=document.documentElement;return{y:Math.round(scrollY),max:Math.max(0,d.scrollHeight-innerHeight),h:innerHeight}}
C.read=function(a){moveXY(innerWidth*0.62,innerHeight*0.38,"reading the page",false,60);if(!a.more||!lines.length){BX=!!a.boxes;build(!!a.viewport)}var out=[],n=0;while(pos<lines.length&&n<(+a.max||6000)){var l=lines[pos];if(n+l.length>(+a.max||6000)&&out.length)break;out.push(l);n+=l.length+1;pos++}
return{ok:true,title:document.title,url:location.pathname+location.search+location.hash,text:out.join("\n"),parts:N,more:pos<lines.length,scroll:scrollInfo()}};
var SIG={},SC={},RES=null;
function allInter(){var out=[];(function w(n){if(n.nodeType!==1||SKIP[n.tagName]===1||/^openrepl-/.test(n.id))return;if(!vis(n))return;if(inter(n)){out.push(n);return}[].forEach.call(n.childNodes,w)})(document.body);return out}
function sigOf(e){return e.tagName+"|"+roleOf(e)+"|"+sh(nm(e),60)}
function find(a){var e=R[a.ref];if(e&&document.contains(e))return e;var g=SIG[a.ref];if(!g)return null;var m=allInter().filter(function(x){return sigOf(x)===g.s});if(!m.length)return null;e=m[Math.min(g.i,m.length-1)];R[a.ref]=e;RES=a.ref;return e}
function gone(a){return{ok:false,error:(a.ref||"that part")+" is not on the page now: read the page again"}}
function ringOn(e,label){try{var r=e.getBoundingClientRect();if(!ring){ring=document.createElement("div");ring.id="openrepl-ring";ring.setAttribute("style","position:fixed;z-index:2147483647;pointer-events:none;border:3px solid #B7E34B;border-radius:6px;box-shadow:0 0 0 4px rgba(183,227,75,.35);box-sizing:border-box;transition:all .15s");var l=document.createElement("div");l.setAttribute("style","position:absolute;left:-3px;background:#15151C;color:#B7E34B;font:600 11px system-ui,sans-serif;padding:2px 7px;border-radius:5px;white-space:nowrap");ring.appendChild(l);document.documentElement.appendChild(ring)}
var s=ring.style;s.left=r.left+"px";s.top=r.top+"px";s.width=Math.max(8,r.width)+"px";s.height=Math.max(8,r.height)+"px";s.display="block";ring.firstChild.textContent=label;ring.firstChild.style.top=r.top<28?(Math.max(8,r.height)+4)+"px":"-26px";clearTimeout(ringT);ringT=setTimeout(function(){if(ring)ring.style.display="none"},2500)}catch(x){}}
var cur=null,curT=0,curSeen=false;
function curEl(){if(!cur){cur=document.createElement("div");cur.id="openrepl-cursor";cur.setAttribute("style","position:fixed;left:0;top:0;z-index:2147483647;pointer-events:none;opacity:0;will-change:transform");cur.innerHTML='<svg width="28" height="28" viewBox="0 0 24 24" style="display:block;filter:drop-shadow(0 2px 3px rgba(0,0,0,.5))"><path d="M4 2l15 9-6.5 1.6L9.6 19z" fill="#B7E34B" stroke="#15151C" stroke-width="1.6" stroke-linejoin="round"/></svg><div style="position:absolute;left:20px;top:22px;background:#15151C;color:#B7E34B;font:600 11px system-ui,sans-serif;padding:2px 7px;border-radius:5px;white-space:nowrap"></div>';document.documentElement.appendChild(cur)}return cur}
function ripple(x,y){try{var r=document.createElement("div");r.id="openrepl-ripple";r.setAttribute("style","position:fixed;z-index:2147483646;pointer-events:none;left:"+(x-6)+"px;top:"+(y-6)+"px;width:12px;height:12px;border-radius:50%;border:3px solid #B7E34B;opacity:.9;transform:scale(1);transition:transform .5s ease-out,opacity .5s ease-out");document.documentElement.appendChild(r);r.offsetWidth;r.style.transform="scale(4)";r.style.opacity="0";setTimeout(function(){r.remove()},600)}catch(e){}}
var from=null;
function moveXY(x,y,label,click,wait){return new Promise(function(res){try{x=Math.max(4,Math.min(innerWidth-30,Math.round(x)));y=Math.max(4,Math.min(innerHeight-30,Math.round(y)));var c=curEl();
if(!curSeen){var f=from||{x:innerWidth-60,y:innerHeight-60};c.style.transition="none";c.style.transform="translate("+f.x+"px,"+f.y+"px)";c.offsetWidth;curSeen=true}
c.style.transition="transform .5s cubic-bezier(.22,.8,.3,1),opacity .2s";c.style.opacity="1";c.style.transform="translate("+x+"px,"+y+"px)";c.lastChild.textContent=label;
post({t:"cursor",x:x,y:y});
clearTimeout(curT);curT=setTimeout(function(){c.style.opacity="0"},6000);
setTimeout(function(){if(click)ripple(x,y);res()},wait||(click?560:520))}catch(z){res()}})}
function moveTo(e,label,click){var r=e.getBoundingClientRect();return moveXY(r.left+Math.min(r.width/2,40),r.top+Math.min(r.height/2,16),label,click)}
C.cursor=function(a){if(a.from)from=a.from;return moveXY(+a.x||innerWidth/2,+a.y||innerHeight/2,String(a.label||"Genie"),false,60).then(function(){return{ok:true}})};
function settle(ms,max){return new Promise(function(res){var t0=Date.now(),last=t0,added=[],mo;try{mo=new MutationObserver(function(rs){last=Date.now();rs.forEach(function(r){[].forEach.call(r.addedNodes,function(n){if(/^openrepl-/.test(n.id))return;var t=(n.textContent||"").replace(/\s+/g," ").trim();if(t&&added.length<5)added.push(sh(t,120))})})});mo.observe(document.documentElement,{childList:true,subtree:true,characterData:true})}catch(e){}
(function tick(){if(Date.now()-last>=ms||Date.now()-t0>=max){try{mo&&mo.disconnect()}catch(e){}res(added)}else setTimeout(tick,50)})()})}
function done(extra){return settle(300,3000).then(function(added){var r={ok:true,url:location.pathname+location.search+location.hash,title:document.title,scroll:scrollInfo()};if(RES){r.rebound=RES;RES=null}if(added.length)r.added=added;if(extra)for(var k in extra)r[k]=extra[k];return r})}
function desc(e){return e.tagName.toLowerCase()+(e.id?"#"+e.id:"")+(e.className&&typeof e.className==="string"?"."+e.className.trim().split(/\s+/).slice(0,2).join("."):"")}
function leaves(e){if(e.tagName!=="A"||!e.href)return false;var p=e.protocol;if(p!=="http:"&&p!=="https:")return !/^#/.test(e.getAttribute("href")||"");return e.host!==location.host}
function sensitive(e){var ty=(e.type||"").toLowerCase(),ac=(e.getAttribute("autocomplete")||"").toLowerCase();return ty==="password"||/^cc-/.test(ac)||/(card.?number|cvv|cvc|security.?code)/i.test((e.name||"")+" "+(e.id||""))}
function submits(e){var t=e.tagName,ty=(e.type||"").toLowerCase();if(!e.form)return false;if(t==="BUTTON")return ty!=="button"&&ty!=="reset";return t==="INPUT"&&(ty==="submit"||ty==="image")}
C.target=function(a){var e=find(a);if(!e)return gone(a);return{ok:true,tag:e.tagName.toLowerCase(),type:(e.type||"").toLowerCase(),name:sh(nm(e),80),submits:submits(e),leaves:leaves(e),href:e.tagName==="A"?e.href:undefined,sensitive:sensitive(e),form:!!e.form,disabled:!!e.disabled,visible:vis(e)}};
function mouse(e,types,x,y,down){types.forEach(function(t){var pressed=down&&/down|move/.test(t);var o={bubbles:true,cancelable:true,composed:true,clientX:x,clientY:y,screenX:x,screenY:y,view:window,button:0,buttons:pressed?1:0};var ev;try{if(/^pointer/.test(t)){o.pointerId=1;o.pointerType="mouse";o.isPrimary=true;o.width=1;o.height=1;o.pressure=pressed?0.5:0;ev=new PointerEvent(t,o)}else ev=new MouseEvent(t,o)}catch(z){ev=new MouseEvent(t,o)}e.dispatchEvent(ev)})}
function scl(e,a){var r=e.getBoundingClientRect();return a.space==="drawing"&&e.tagName==="CANVAS"&&e.width&&e.height?{x:r.width/e.width,y:r.height/e.height}:{x:1,y:1}}
function spot(e,a){var r=e.getBoundingClientRect(),k=scl(e,a);return[r.left+(a.x==null?r.width/2:Math.max(0,Math.min(r.width,(+a.x||0)*k.x))),r.top+(a.y==null?r.height/2:Math.max(0,Math.min(r.height,(+a.y||0)*k.y)))]}
function centre(e){var r=e.getBoundingClientRect();return[r.left+r.width/2,r.top+r.height/2]}
C.click=function(a){var e=find(a);if(!e)return gone(a);if(e.disabled)return{ok:false,error:a.ref+" is disabled"};
e.scrollIntoView({block:"center",inline:"nearest"});var c=spot(e,a),top=document.elementFromPoint(c[0],c[1]);
if(top&&top!==e&&!e.contains(top)&&!top.contains(e)&&!a.force)return{ok:false,error:a.ref+" is covered by "+desc(top)+": inspect it, or scroll or close what covers it"};
if(leaves(e)&&!a.allowLeave)return{ok:false,leaves:true,error:"this link leaves the workspace: "+e.href};
ringOn(e,a.ref+" - click");
return moveXY(c[0],c[1],a.ref+" - click",true).then(function(){try{e.focus()}catch(z){}
mouse(e,["pointerover","mouseover","pointerenter","mouseenter","pointermove","mousemove"],c[0],c[1]);mouse(e,["pointerdown","mousedown"],c[0],c[1],true);mouse(e,["pointerup","mouseup"],c[0],c[1]);if(a.x==null&&a.y==null)e.click();else mouse(e,["click"],c[0],c[1]);return done()})};
function sleep(ms){return new Promise(function(r){setTimeout(r,ms)})}
C.drag=function(a){var e=find(a);if(!e)return gone(a);if(e.disabled)return{ok:false,error:a.ref+" is disabled"};
e.scrollIntoView({block:"center",inline:"nearest"});var p=spot(e,a),k=scl(e,a),dx=(+a.dx||0)*k.x,dy=(+a.dy||0)*k.y,n=Math.max(2,Math.min(40,Math.round(+a.steps||12)));
if(!dx&&!dy)return{ok:false,error:"preview_drag needs dx or dy: how far to drag, in pixels"};
ringOn(e,a.ref+" - drag");
return moveXY(p[0],p[1],a.ref+" - drag",false).then(function(){try{e.focus()}catch(z){}
mouse(e,["pointerover","mouseover","pointerenter","mouseenter","pointermove","mousemove"],p[0],p[1]);mouse(e,["pointerdown","mousedown"],p[0],p[1],true);
var i=0;function step(){i++;var x=p[0]+dx*i/n,y=p[1]+dy*i/n;mouse(e,["pointermove","mousemove"],x,y,true);if(i<n)return sleep(16).then(step);return moveXY(x,y,a.ref+" - drop",false,80).then(function(){mouse(e,["pointerup","mouseup"],x,y);return done({dragged:[Math.round(dx),Math.round(dy)]})})}
return step()})};
function key(e,k,mods){var o={key:k,code:/^[a-z]$/i.test(k)?"Key"+k.toUpperCase():k,bubbles:true,cancelable:true,ctrlKey:!!(mods&&mods.ctrl),shiftKey:!!(mods&&mods.shift),altKey:!!(mods&&mods.alt),metaKey:!!(mods&&mods.meta)};var d=e.dispatchEvent(new KeyboardEvent("keydown",o));if(k.length===1)e.dispatchEvent(new KeyboardEvent("keypress",o));e.dispatchEvent(new KeyboardEvent("keyup",o));return d}
C.type=function(a){var e=find(a);if(!e)return gone(a);if(sensitive(e))return{ok:false,error:"refused: "+a.ref+" is a password or card field"};if(e.disabled||e.readOnly)return{ok:false,error:a.ref+" cannot be typed in"};
e.scrollIntoView({block:"center",inline:"nearest"});ringOn(e,a.ref+" - type");
return moveTo(e,a.ref+" - type",true).then(function(){return typeNow(e,a)})};
function typeNow(e,a){try{e.focus()}catch(z){}
var tx=String(a.text==null?"":a.text);
if(e.tagName==="INPUT"||e.tagName==="TEXTAREA"){var d=Object.getOwnPropertyDescriptor(e.tagName==="TEXTAREA"?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,"value"),nv=a.append?String(e.value||"")+tx:tx;if(d&&d.set)d.set.call(e,nv);else e.value=nv}
else if(e.isContentEditable){e.textContent=a.append?(e.textContent||"")+tx:tx}else return{ok:false,error:a.ref+" is not a field"};
e.dispatchEvent(new Event("input",{bubbles:true}));e.dispatchEvent(new Event("change",{bubbles:true}));
if(a.enter){if(key(e,"Enter")&&e.form&&e.tagName==="INPUT"){try{e.form.requestSubmit?e.form.requestSubmit():e.form.submit()}catch(z){}}}
return done()}
var KEYS={enter:"Enter",esc:"Escape",escape:"Escape",tab:"Tab",space:" ",backspace:"Backspace",delete:"Delete",up:"ArrowUp",down:"ArrowDown",left:"ArrowLeft",right:"ArrowRight",home:"Home",end:"End",pageup:"PageUp",pagedown:"PageDown"};
function focusables(){return[].filter.call(document.querySelectorAll("a[href],button,input,select,textarea,summary,[tabindex],[contenteditable]"),function(e){return !e.disabled&&e.tabIndex>=0&&vis(e)})}
C.press=function(a){var raw=String(a.key||""),ps=raw.split("+"),k=ps.pop(),mods={};ps.forEach(function(m){m=m.toLowerCase();if(m==="ctrl"||m==="control")mods.ctrl=1;else if(m==="shift")mods.shift=1;else if(m==="alt")mods.alt=1;else if(m==="meta"||m==="cmd")mods.meta=1});
var kk=KEYS[k.toLowerCase()]||k;if(!kk)return{ok:false,error:"no key"};var e=a.ref?find(a):(document.activeElement&&document.activeElement!==document.body?document.activeElement:document.body);if(!e)return gone(a);
if(a.ref)ringOn(e,a.ref+" - key "+raw);return(a.ref?moveTo(e,a.ref+" - key "+raw,false):moveXY(innerWidth*0.6,innerHeight*0.5,"key "+raw,false)).then(function(){return pressNow(e,kk,mods,a)})};
function pressNow(e,kk,mods,a){var ok=key(e,kk,mods);
if(ok&&kk==="Tab"){var f=focusables(),i=f.indexOf(document.activeElement);var n=f[(i+(mods.shift?-1:1)+f.length)%f.length];if(n)n.focus()}
else if(ok&&kk==="Enter"&&(e.tagName==="A"||e.tagName==="BUTTON"||e.tagName==="SUMMARY")){if(!leaves(e))e.click()}
return done({focus:document.activeElement&&document.activeElement!==document.body?desc(document.activeElement):""})}
C.scroll=function(a){if(a.ref){var e=find(a);if(!e)return gone(a);ringOn(e,a.ref+" - scroll");return moveXY(innerWidth*0.6,innerHeight*0.5,a.ref+" - scroll",false).then(function(){e.scrollIntoView({block:"center",behavior:"auto"});return settle(150,800).then(function(){return{ok:true,scroll:scrollInfo()}})})}
return moveXY(innerWidth*0.6,innerHeight*0.5,"scroll "+String(a.to||"down"),false).then(function(){return scrollNow(a)})};
function scrollNow(a){{var t=String(a.to||"down");if(t==="top")scrollTo(0,0);else if(t==="bottom"||t==="end")scrollTo(0,document.documentElement.scrollHeight);else if(t==="up")scrollBy(0,-Math.round(innerHeight*0.8));else scrollBy(0,Math.round(innerHeight*0.8))}
return settle(150,800).then(function(){return{ok:true,scroll:scrollInfo()}})}
C.select=function(a){var e=find(a);if(!e)return gone(a);e.scrollIntoView({block:"center"});ringOn(e,a.ref+" - select");return moveTo(e,a.ref+" - select",false).then(function(){return selectNow(e,a)})};
function selectNow(e,a){
if(e.tagName==="SELECT"){var v=String(a.value==null?"":a.value),hit=-1;[].forEach.call(e.options,function(o,i){if(hit<0&&(o.value===v||o.text.trim()===v))hit=i});if(hit<0)[].forEach.call(e.options,function(o,i){if(hit<0&&o.text.toLowerCase().indexOf(v.toLowerCase())>=0)hit=i});if(hit<0)return{ok:false,error:"no option "+q(v)+" in "+a.ref};e.selectedIndex=hit;e.dispatchEvent(new Event("input",{bubbles:true}));e.dispatchEvent(new Event("change",{bubbles:true}));return done({value:e.options[hit].text})}
var ty=(e.type||"").toLowerCase();if(ty==="checkbox"||ty==="radio"){var want=a.checked===undefined?true:!!a.checked;if(e.checked!==want)e.click();return done({checked:e.checked})}
return{ok:false,error:a.ref+" is not a list, a box or a button to tick"}}
C.hover=function(a){var e=find(a);if(!e)return gone(a);e.scrollIntoView({block:"center"});var c=centre(e);ringOn(e,a.ref+" - hover");return moveTo(e,a.ref+" - hover",false).then(function(){mouse(e,["pointerover","mouseover","pointerenter","mouseenter","pointermove","mousemove"],c[0],c[1]);return done()})};
C.wait=function(a){moveXY(innerWidth*0.6,innerHeight*0.45,"waiting",false,60);var tm=Math.min(10000,Math.max(200,(+a.timeout||5)*1000)),t0=Date.now();return new Promise(function(res){(function tick(){var txt=document.body?document.body.innerText:"",ok;
if(a.text)ok=a.gone?txt.indexOf(a.text)<0:txt.indexOf(a.text)>=0;else if(a.ref){ok=a.gone?!find(a):!!find(a)}else{ok=true}
if(ok&&!a.text&&!a.ref){settle(500,tm).then(function(){res({ok:true,quiet:true,url:location.pathname+location.search+location.hash})});return}
if(ok){res({ok:true,url:location.pathname+location.search+location.hash});return}
if(Date.now()-t0>=tm){res({ok:false,timeout:true,error:"not there after "+Math.round(tm/1000)+" s"});return}
setTimeout(tick,100)})()})};
C.inspect=function(a){var e=find(a);if(!e)return gone(a);var r=e.getBoundingClientRect(),s=getComputedStyle(e),c=centre(e),top=r.width&&r.height?document.elementFromPoint(c[0],c[1]):null,st={};
["display","position","visibility","opacity","color","background-color","font-size","z-index","overflow","pointer-events","width","height"].forEach(function(k){st[k]=s.getPropertyValue(k)});
var at={};["id","class","href","type","name","role","aria-label","aria-hidden","disabled","placeholder"].forEach(function(k){var v=e.getAttribute(k);if(v!==null)at[k]=sh(v,100)});
return{ok:true,tag:e.tagName.toLowerCase(),rect:{x:Math.round(r.left),y:Math.round(r.top),w:Math.round(r.width),h:Math.round(r.height)},page:{x:Math.round(r.left+scrollX),y:Math.round(r.top+scrollY)},drawing:e.tagName==="CANVAS"?{w:e.width,h:e.height}:undefined,inView:inView(e),visible:vis(e)&&r.width>0&&r.height>0,coveredBy:top&&top!==e&&!e.contains(top)&&!top.contains(e)?desc(top):"",disabled:!!e.disabled,styles:st,attrs:at,text:sh((e.textContent||"").replace(/\s+/g," ").trim(),160)}};
C.find=function(a){var list=[],sel=a.selector,tx=a.text;
if(a.left!=null&&a.top!=null){var p=document.elementFromPoint(+a.left,+a.top);if(p)list=[p]}
else if(sel){try{list=[].slice.call(document.querySelectorAll(String(sel)),0,80)}catch(z){return{ok:false,error:"not a CSS selector: "+z.message}}}
else if(tx){var w=String(tx).toLowerCase();list=[].filter.call(document.querySelectorAll("body *"),function(e){if(SKIP[e.tagName]===1||/^openrepl-/.test(e.id)||!vis(e))return false;var own=[].filter.call(e.childNodes,function(n){return n.nodeType===3}).map(function(n){return n.nodeValue}).join(" ").replace(/\s+/g," ").trim().toLowerCase();return own&&own.indexOf(w)>=0}).slice(0,80)}
else return{ok:false,error:"preview_find needs a selector, a text, or left and top"};
list=list.filter(function(e){return !/^openrepl-/.test(e.id)});
var items=list.slice(0,20).map(function(e){var id=null;for(var k in R){if(R[k]===e){id=k;break}}if(!id){id="e"+(++N);R[id]=e}var r=e.getBoundingClientRect(),cl=typeof e.className==="string"?e.className.trim().split(/\s+/).slice(0,3).join("."):"";return{ref:id,tag:e.tagName.toLowerCase(),id:e.id||undefined,cls:cl||undefined,x:Math.round(r.left+scrollX),y:Math.round(r.top+scrollY),w:Math.round(r.width),h:Math.round(r.height),visible:vis(e)&&r.width>0&&r.height>0,text:sh((e.textContent||"").replace(/\s+/g," ").trim(),50)}});
return{ok:true,count:list.length,items:items,scroll:{x:Math.round(scrollX),y:Math.round(scrollY)},viewport:{w:innerWidth,h:innerHeight}}};
function ser(v){var seen=[];try{var s=JSON.stringify(v,function(k,x){if(typeof x==="function")return"[function]";if(typeof x==="undefined")return"[undefined]";if(typeof x==="bigint")return String(x);if(x&&typeof x==="object"){if(x.nodeType===1)return"[element "+desc(x)+"]";if(x.nodeType)return"[node]";if(seen.indexOf(x)>=0)return"[circular]";seen.push(x)}return x});return s===undefined?"undefined":sh(s,4000)}catch(e){return sh(String(v),4000)}}
C.eval=function(a){var code=String(a.code||""),fn;try{fn=new Function("return ("+code+"\n)")}catch(x){try{fn=new Function(code)}catch(y){return{ok:false,error:"syntax error: "+y.message}}}
var out;try{out=fn()}catch(x){return{ok:false,error:sh(x&&x.stack||x,500)}}
return Promise.race([Promise.resolve(out),new Promise(function(r,j){setTimeout(function(){j(new Error("timed out after 5 s"))},5000)})]).then(function(v){return{ok:true,value:ser(v)}},function(x){return{ok:false,error:sh(x&&x.message||x,500)}})};
C.storage=function(a){var area=a.area||"local",op=a.op||"get",s;try{
if(area==="cookie"){if(op==="get")return{ok:true,value:sh(document.cookie,4000)};if(op==="set"){document.cookie=String(a.key)+"="+String(a.value==null?"":a.value);return{ok:true}}if(op==="remove"){document.cookie=String(a.key)+"=; expires=Thu, 01 Jan 1970 00:00:00 GMT";return{ok:true}}return{ok:false,error:"unknown operation"}}
s=area==="session"?sessionStorage:localStorage;
if(op==="get"){if(a.key!==undefined&&a.key!=="")return{ok:true,value:s.getItem(String(a.key))};var o={},n=Math.min(s.length,50);for(var i=0;i<n;i++){var k=s.key(i);o[k]=sh(s.getItem(k),300)}return{ok:true,count:s.length,value:o}}
if(op==="set"){s.setItem(String(a.key),String(a.value==null?"":a.value));return{ok:true}}
if(op==="remove"){s.removeItem(String(a.key));return{ok:true}}
if(op==="clear"){s.clear();return{ok:true}}return{ok:false,error:"unknown operation"}}catch(x){return{ok:false,error:sh(x&&x.message||x,300)}}};
addEventListener("message",function(e){var d=e.data;if(e.source!==parent||!d||typeof d.openreplCmd!=="string")return;var id=d.id,f=C[d.openreplCmd];if(!f){reply(id,{ok:false,error:"unknown command"});return}
if(d.from&&typeof d.from.x==="number")from=d.from;
var p;try{p=Promise.resolve(f(d))}catch(x){p=Promise.resolve({ok:false,error:sh(x&&x.message||x,300)})}
p.then(function(r){reply(id,r)},function(x){reply(id,{ok:false,error:sh(x&&x.message||x,300)})})});
var WO=window.open;window.open=function(u){post({t:"popup",url:sh(u,300)});return WO.apply(this,arguments)};
var CO=URL.createObjectURL,blobs={};URL.createObjectURL=function(b){var u=CO.apply(URL,arguments);try{if(b&&b.size<=20000)blobs[u]=b}catch(e){}return u};
addEventListener("click",function(e){var a=e.target&&e.target.closest?e.target.closest("a[download]"):null;if(!a)return;var u=a.href,m={t:"download",name:sh(a.getAttribute("download")||String(u).split("/").pop(),100),url:sh(u,200)},b=blobs[u];if(b){m.size=b.size;if(b.text){b.text().then(function(t){m.text=sh(t,20000);post(m)},function(){post(m)});return}}post(m)},true);
})();</script>`
