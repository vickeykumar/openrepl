// Sign-in: FirebaseUI, the /login session, the account menu and logout.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.logout = logout;
window.renderProfileData = renderProfileData;

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
      var url = window.location.protocol + "//" + window.location.host + "/logout";
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
      var url = window.location.protocol + "//" + window.location.host + "/profile?q=json";
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
              // who is signed in, at the top of the account menu
              var who = document.getElementById('account-who');
              if ( who && (user.displayName || user.email) ) {
                document.getElementById('account-name').textContent = user.displayName || String(user.email).split('@')[0];
                document.getElementById('account-email').textContent = user.email || '';
                who.hidden = false;
              }
              // admins get a link to the dashboard in the account menu
              var menu = document.getElementById('account-dropdown');
              if ( user.isAdmin === true && menu && !document.getElementById('admin-link') ) {
                var link = document.createElement('a');
                link.id = 'admin-link';
                link.href = './admin';
                link.setAttribute('role', 'menuitem');
                // the icon is a constant, the label is text
                link.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg>';
                link.appendChild(document.createTextNode('Admin'));
                var profileLink = menu.querySelector('a[href="./profile"]');
                menu.insertBefore(link, profileLink ? profileLink.nextSibling : menu.firstChild);
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
    // FirebaseUI loads the first time someone signs in (T16).
    var ui = null;
    function ensureAuthUI() {
      return Promise.all([loadStyleOnce(FIREBASEUI_CSS), loadScriptOnce(FIREBASEUI_JS)]).then(function () {
        // FirebaseUI restores the credential it saved (the GitHub sign-in of an address that
        // already has an account, to link the two) with AuthCredential.fromJSON, which the v9
        // compat SDK does not have. Without it FirebaseUI throws and draws nothing.
        if (firebase.auth.AuthCredential && typeof firebase.auth.AuthCredential.fromJSON !== 'function') {
          firebase.auth.AuthCredential.fromJSON = function (json) {
            try { return firebase.auth.OAuthProvider.credentialFromJSON(json); } catch (e) { return null; }
          };
        }
        if (!ui) ui = new firebaseui.auth.AuthUI(firebase.auth());
        return ui;
      });
    }

    // ---- the sign-in dialog (index.html #signin-dialog, ui-refresh.css "Sign-in dialog") ----
    // FirebaseUI draws the sign-in methods and the email steps into
    // #firebaseui-auth-container. The dialog around it keeps its state in data
    // attributes that the CSS reads:
    //   data-mode  signin | signup     the heading and the switch link
    //   data-step  pick | flow | verify  the method list, an email step, "check your inbox"
    //   data-state ready | loading | error
    var dialog = document.getElementById('signin-dialog');
    var container = document.getElementById('firebaseui-auth-container');
    var loadingText = document.getElementById('signin-loading-text');
    var MODES = {
      signin: { title: 'Sign in to OpenREPL', lead: 'Your files are kept between visits, and Genie allows you more requests.' },
      signup: { title: 'Create your free account', lead: 'It is free. Your files are kept between visits, and Genie allows you more requests.' }
    };
    var loadTimer = null, verifyTimer = null, mode = 'signin';

    // Google and GitHub sign in through a popup. When the browser refuses the
    // popup, FirebaseUI redirects the whole page to the provider instead, and the
    // browser returns to this page without a dialog. FirebaseUI can only finish
    // that sign-in if it is started again, so a sign-in with a provider is
    // remembered (for this tab) until it ends, and the dialog opens again on
    // return.
    var PENDING_KEY = 'openrepl-signin-pending';
    function setPending(on) {
      try {
        if (on) sessionStorage.setItem(PENDING_KEY, mode);
        else sessionStorage.removeItem(PENDING_KEY);
      } catch (e) { /* storage may be blocked; the popup flow does not need it */ }
    }
    function takePending() {
      try {
        var m = sessionStorage.getItem(PENDING_KEY);
        sessionStorage.removeItem(PENDING_KEY);
        return m;
      } catch (e) { return null; }
    }

    function setAttr(name, value) { if (dialog) dialog.setAttribute('data-' + name, value); }
    // patient: waiting for something the visitor does (a provider's window), or for the
    // server to answer. Otherwise a dialog that stays loading turns into the error state.
    var waitCancel = document.getElementById('signin-wait-cancel');
    var waitTimer = null;
    function setState(state, text, patient) {
      clearTimeout(loadTimer);
      clearTimeout(waitTimer);
      if (loadingText) loadingText.textContent = text || 'Loading sign-in…';
      if (waitCancel) waitCancel.hidden = !(patient === 'provider');
      setAttr('state', state);
      // never leave the dialog loading for ever
      if (state === 'loading' && !patient) loadTimer = setTimeout(function () { setAttr('state', 'error'); }, 20000);
    }

    // What Firebase answers when a request fails (a wrong client secret, an unauthorized
    // domain, ...) is hidden by FirebaseUI behind a generic message. Show it under the
    // methods, and in the console, so a sign-in that does not work says why.
    var detail = document.getElementById('signin-detail');
    function showDetail(lines, hints) {
      if (!detail) return;
      detail.textContent = '';
      lines.concat(hints || []).forEach(function (text, i) {
        var li = document.createElement('li');
        if (i >= lines.length) li.className = 'signin__hint';
        li.textContent = text;
        detail.appendChild(li);
      });
      detail.hidden = detail.children.length === 0;
    }
    var authProblems = [];
    // What happened since the visitor chose a provider, step by step, so that a sign-in
    // that ends with nothing to show can say how far it got.
    var trail = [];
    function note(line) {
      console.log('sign-in: ' + line);
      if (trail[trail.length - 1] !== line) trail.push(line);
    }
    function noteAuthProblem(url, status, message) {
      var call = (url.match(/accounts:(\w+)/) || url.match(/\/(v1\/[\w:]+)/) || [])[1] || 'auth';
      var line = 'Firebase said: ' + (message || 'the request failed') + ' (' + call + ', ' + (status || 'no answer') + ')';
      console.log(line);
      if (authProblems.indexOf(line) < 0) authProblems.push(line);
      showDetail(authProblems.slice(-2));
    }
    if (window.fetch && !window.__openreplAuthWatch) {
      window.__openreplAuthWatch = true;
      var plainFetch = window.fetch;
      window.fetch = function (input) {
        var url = typeof input === 'string' ? input : (input && input.url) || '';
        var result = plainFetch.apply(this, arguments);
        if (/identitytoolkit\.googleapis\.com|securetoken\.googleapis\.com/.test(url)) {
          result.then(function (r) {
            if (r.ok) return;
            return r.clone().json().then(function (body) {
              noteAuthProblem(url, r.status, body && body.error && body.error.message);
            }, function () { noteAuthProblem(url, r.status, ''); });
          }, function (err) { noteAuthProblem(url, 0, String(err && err.message || err)); });
        }
        return result;
      };
    }

    // The dialog waits while the provider's window is open (waitForProvider). Whatever
    // FirebaseUI draws next, a page that asks something of the visitor (link this
    // account, an error) or the method list again, ends that wait.
    function waitingForProvider() {
      return dialog.getAttribute('data-state') === 'loading' && waitCancel && !waitCancel.hidden;
    }

    // How the provider's window ended. FirebaseUI answers most endings itself, and
    // some with nothing at all (the window was closed), which would leave the dialog
    // waiting. The ones that point at a setup problem are shown under the methods.
    var BENIGN_ENDINGS = ['auth/popup-closed-by-user', 'auth/cancelled-popup-request', 'auth/account-exists-with-different-credential'];
    // On the class, not on the page's auth object: FirebaseUI signs in through a copy of the
    // app of its own. The original is kept on the class; the wrapper only watches.
    var AuthClass = window.firebase && firebase.auth && firebase.auth.Auth;
    if (AuthClass && AuthClass.prototype.signInWithPopup && !AuthClass.prototype.__plainSignInWithPopup) {
      AuthClass.prototype.__plainSignInWithPopup = AuthClass.prototype.signInWithPopup;
      AuthClass.prototype.signInWithPopup = function () {
        var result = this.__plainSignInWithPopup.apply(this, arguments);
        result.then(function (cred) {
          var user = cred && cred.user;
          note('The provider\'s window finished' + (user ? ' (email ' + (user.emailVerified ? '' : 'not ') + 'verified)' : ''));
          // FirebaseUI hands the account over next; if it never does, do not wait for ever
          setTimeout(function () { if (waitingForProvider()) { setState('ready'); checkBlank(); } }, 8000);
        }, function (err) {
          var code = (err && err.code) || String(err);
          note('The provider\'s window ended: ' + code);
          if (BENIGN_ENDINGS.indexOf(code) < 0) {
            authProblems.push('The sign-in window ended with ' + code + '.');
            showDetail(authProblems.slice(-2));
          }
          // closed by the visitor: FirebaseUI leaves the method list as it was
          if (code === 'auth/popup-closed-by-user' && waitingForProvider()) setState('ready');
          // anything else: FirebaseUI shows a page next, which the observer lets through;
          // if it shows nothing, do not leave the visitor waiting
          else if (code !== 'auth/cancelled-popup-request') {
            setTimeout(function () { if (waitingForProvider()) setState('ready'); checkBlank(); }, 4000);
          }
        });
        return result;
      };
    }

    // Can the browser reach what the provider's window needs? An extension that blocks
    // Google's script, or a network that blocks Firebase, leaves that window blank.
    function reachable(url) {
      return new Promise(function (resolve) {
        var done = false, ctl = window.AbortController ? new AbortController() : null;
        var t = setTimeout(function () { if (!done) { done = true; if (ctl) ctl.abort(); resolve(false); } }, 8000);
        fetch(url, { mode: 'no-cors', cache: 'no-store', signal: ctl ? ctl.signal : undefined })
          .then(function () { if (!done) { done = true; clearTimeout(t); resolve(true); } },
                function () { if (!done) { done = true; clearTimeout(t); resolve(false); } });
      });
    }
    function probeSignIn() {
      var domain = (typeof firebaseconfig !== 'undefined' && firebaseconfig.authDomain) || '';
      var checks = [['Google\'s sign-in script (apis.google.com)', 'https://apis.google.com/js/api.js']];
      if (domain) checks.push(['Firebase sign-in pages (' + domain + ')', 'https://' + domain + '/__/auth/iframe']);
      Promise.all(checks.map(function (c) { return reachable(c[1]); })).then(function (ok) {
        var blocked = checks.filter(function (c, i) { return !ok[i]; }).map(function (c) { return c[0] + ' cannot be reached from this browser.'; });
        var hints = blocked.length ? ['An extension or the network is blocking it. Try a private window with extensions off.']
          : ['If its window stays blank, the browser may block third-party storage or cookies for ' + (domain || 'Firebase') + '. Try a private window with extensions off, or another browser.'];
        showDetail(authProblems.slice(-2).concat(blocked), hints);
      });
    }

    // The visitor chose Google or GitHub: FirebaseUI opens the provider's window and has
    // nothing to show meanwhile, so say what is going on, and what to do when it stalls.
    function waitForProvider(name) {
      var text = 'Signing in with ' + name + '. Finish in the window that opened.';
      authProblems = [];
      showDetail([]);
      setState('loading', text, 'provider');
      waitTimer = setTimeout(function () {
        if (loadingText) loadingText.textContent = 'Still waiting for ' + name + '. If its window is blank or closed, cancel and try again.';
        probeSignIn();
      }, 30000);
    }
    function setMode(next) {
      mode = MODES[next] ? next : 'signin';
      setAttr('mode', mode);
      document.getElementById('signin-title').textContent = MODES[mode].title;
      document.getElementById('signin-lead').textContent = MODES[mode].lead;
    }

    // Once signed in with Firebase, the site makes its own session from the result.
    function completeLogin(authResult) {
      setState('loading', 'Signing you in…', true);
      var xhr = new XMLHttpRequest();
      var url = window.location.protocol + "//" + window.location.host + "/login";
      xhr.open("POST", url, true);
      xhr.setRequestHeader("Content-Type", "application/json");
      xhr.onreadystatechange = function () {
          if (xhr.readyState === 4) {
              note('The site answered the sign-in with ' + xhr.status);
              if (xhr.status === 200) {
                  console.log("login success");
              } else {
                  notify("Please try again.", { type: "error", title: "Sign-in failed" });
              }
              // redirect anyway
              location.reload();
          }
          console.log("login status: ",xhr.status);
      };
      xhr.send(JSON.stringify(authResult));
    }

    function el(tag, cls, text) {
      var e = document.createElement(tag);
      if (cls) e.className = cls;
      if (text !== undefined) e.textContent = text;
      return e;
    }

    // The address is not verified yet: say so, and let the user resend the link,
    // carry on once they have opened it, or pick another account.
    function showVerifyPanel(authResult) {
      var user = authResult.user;
      var isNew = !!(authResult.additionalUserInfo && authResult.additionalUserInfo.isNewUser);
      clearInterval(verifyTimer);
      setAttr('step', 'verify');
      setState('ready');
      resetUI();
      container.textContent = '';

      var panel = el('div', 'signin__verify');
      var icon = el('div', 'signin__verify-icon');
      icon.innerHTML = '<svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2"></rect><path d="M3 7l9 6 9-6"></path></svg>';
      var text = el('p', 'signin__verify-text');
      if (isNew) {
        text.appendChild(document.createTextNode('We sent a link to '));
        text.appendChild(el('b', '', user.email || 'your address'));
        text.appendChild(document.createTextNode('. Open it, then come back here.'));
      } else {
        text.appendChild(document.createTextNode('Your address '));
        text.appendChild(el('b', '', user.email || ''));
        text.appendChild(document.createTextNode(' has not been verified yet. Send the link, open it, then come back here.'));
      }
      var status = el('p', 'signin__verify-status');
      status.setAttribute('role', 'status');
      var done = el('button', 'signin__btn signin__btn--primary', "I've verified my email");
      var again = el('button', 'signin__btn', 'Send the link again');
      var other = el('button', 'signin__btn', 'Use a different account');
      [done, again, other].forEach(function (b) { b.type = 'button'; });

      panel.appendChild(icon);
      panel.appendChild(el('h3', 'signin__verify-title', isNew ? 'Check your inbox' : 'Verify your email'));
      panel.appendChild(text);
      panel.appendChild(status);
      var actions = el('div', 'signin__verify-actions');
      actions.appendChild(done);
      actions.appendChild(again);
      actions.appendChild(other);
      panel.appendChild(actions);
      panel.appendChild(el('p', 'signin__verify-note', "Look in your spam folder if you don't see it."));
      container.appendChild(panel);
      done.focus();

      // one link a half minute: the button counts down so it is not pressed in vain
      function cooldown(seconds) {
        var left = seconds;
        again.disabled = true;
        clearInterval(verifyTimer);
        verifyTimer = setInterval(function () {
          left--;
          if (left <= 0) {
            clearInterval(verifyTimer);
            again.disabled = false;
            again.textContent = 'Send the link again';
          } else {
            again.textContent = 'Send again in ' + left + 's';
          }
        }, 1000);
        again.textContent = 'Send again in ' + left + 's';
      }
      function send() {
        status.textContent = 'Sending…';
        return user.sendEmailVerification().then(function () {
          status.textContent = 'Link sent. It can take a minute to arrive.';
          cooldown(30);
        }, function () {
          status.textContent = "Couldn't send the link. Try again in a minute.";
          cooldown(30);
        });
      }
      if (isNew) {
        send();
      } else {
        status.textContent = '';
      }
      again.addEventListener('click', send);
      done.addEventListener('click', function () {
        done.disabled = true;
        status.textContent = 'Checking…';
        user.reload().then(function () {
          done.disabled = false;
          var fresh = firebase.auth().currentUser;
          if (fresh && fresh.emailVerified) {
            completeLogin({ user: fresh });
          } else {
            status.textContent = 'Not verified yet. Open the link in the email first.';
          }
        }, function () {
          done.disabled = false;
          status.textContent = "Couldn't check just now. Try again.";
        });
      });
      other.addEventListener('click', function () {
        clearInterval(verifyTimer);
        firebase.auth().signOut().then(startUI, startUI);
      });
    }

    var uiConfig = {
      callbacks: {
        signInSuccessWithAuthResult: function(authResult, redirectUrl) {
          // User successfully signed in; the return value keeps FirebaseUI from redirecting.
          setPending(false);
          note('FirebaseUI handed over the account (email ' + (authResult.user && authResult.user.emailVerified ? '' : 'not ') + 'verified)');
          if (authResult.user && authResult.user.emailVerified) {
            completeLogin(authResult);
          } else {
            showVerifyPanel(authResult);
          }
          return false;
        },
        uiShown: function () {
          setState('ready');
          // the dialog opens on its close button; the first thing to do is pick a method
          var first = container.querySelector('.firebaseui-idp-button');
          if (first && dialog.contains(document.activeElement) && document.activeElement.id === 'signin-close') first.focus();
        }
      },
      // Will use popup for IDP Providers sign-in flow instead of the default, redirect.
      signInFlow: 'popup',
      signInSuccessUrl: '/',
      signInOptions: [
        // Leave the lines as is for the providers you want to offer your users.
        // The order is the order on the page; email comes last, below an "or".
        { provider: firebase.auth.GoogleAuthProvider.PROVIDER_ID, fullLabel: 'Continue with Google' },
        { provider: firebase.auth.GithubAuthProvider.PROVIDER_ID, fullLabel: 'Continue with GitHub' },
        { provider: firebase.auth.EmailAuthProvider.PROVIDER_ID, fullLabel: 'Continue with email' }
        //firebase.auth.FacebookAuthProvider.PROVIDER_ID,
        //firebase.auth.TwitterAuthProvider.PROVIDER_ID,
        //firebase.auth.PhoneAuthProvider.PROVIDER_ID
      ],
      // Terms of service url.
      tosUrl: '/terms.html',
      // Privacy policy url.
      privacyPolicyUrl: '/privacy.html'
    };

    // FirebaseUI replaces what is in the container at every step. The method
    // list and the email steps need different surroundings (the email steps
    // have a title of their own), so watch which one is on show.
    if (container) {
      // in the capture phase: FirebaseUI's own handler may stop the event, and the
      // page can be gone as soon as it has run
      container.addEventListener('click', function (e) {
        var b = e.target.closest && e.target.closest('.firebaseui-idp-button');
        if (b && !b.classList.contains('firebaseui-idp-password')) {
          trail = [];
          setPending(true);
          waitForProvider(b.classList.contains('firebaseui-idp-github') ? 'GitHub' : b.classList.contains('firebaseui-idp-google') ? 'Google' : 'your provider');
        }
      }, true);
    }
    if (container && window.MutationObserver) {
      new MutationObserver(function () {
        if (dialog.getAttribute('data-step') === 'verify') return;
        var box = container.querySelector('.firebaseui-container');
        if (!box) {
          if (!container.firstElementChild) setTimeout(checkBlank, 1500);
          return;
        }
        var page = (box.className.match(/firebaseui-id-page-([a-z-]+)/) || [])[1] || '';
        if (trail.length) note('FirebaseUI showed: ' + page);
        var onList = !!container.querySelector('.firebaseui-idp-list');
        setAttr('step', onList ? 'pick' : 'flow');
        // FirebaseUI is done with the provider's window when it shows the list again
        // (closed or refused) or a page for the visitor (link the accounts, an error);
        // its own spinner pages are still part of the wait
        if (waitingForProvider() && !/^(callback|spinner|blank)$/.test(page)) setState('ready');
      }).observe(container, { childList: true, subtree: true });
    }

    // FirebaseUI forgets what it showed; a failure here must not stop the next start
    function resetUI() {
      try { if (ui) ui.reset(); } catch (e) { console.log("firebaseui reset: ", e); }
    }

    // FirebaseUI drew nothing after a provider (the visitor sees only the logo): say how
    // far the sign-in got, and show the methods again.
    function checkBlank() {
      if (!dialog.open || container.firstElementChild) return;
      if (dialog.getAttribute('data-step') === 'verify' || dialog.getAttribute('data-state') !== 'ready') return;
      showDetail(trail.slice(-6), ['That did not finish. Choose a method to try again.']);
      startUI(true);
    }

    function startUI(keepDetail) {
      setAttr('step', 'pick');
      if (keepDetail !== true) {
        authProblems = [];
        showDetail([]);
      }
      setState('loading');
      ensureAuthUI().then(function (authUI) {
        resetUI();
        authUI.start('#firebaseui-auth-container', uiConfig);
      }).catch(function (err) {
        console.log("sign-in could not start: ", err);
        setState('error');
      });
    }

    // Everything the dialog holds is dropped when it closes, so the next time
    // it opens it starts from the method list.
    function closeSignIn() {
      setPending(false);
      if (dialog.open) dialog.close();
      clearTimeout(loadTimer);
      clearInterval(verifyTimer);
      resetUI();
      container.textContent = '';
    }

    function openSignIn(next) {
      if (!dialog) return;
      setMode(next);
      if (!dialog.open) {
        if (dialog.showModal) dialog.showModal(); else dialog.setAttribute('open', '');
      }
      startUI();
    }

    // other scripts can open the dialog; the page globals are what they reach
    window.openSignIn = openSignIn;
    window.showVerifyPanel = showVerifyPanel;

    if (dialog) {
      // Esc closes the dialog without going through closeSignIn. The close event
      // can arrive after the dialog was opened again, so only a closed dialog is cleaned up.
      dialog.addEventListener('close', function () { if (!dialog.open) closeSignIn(); });
      // a click on the dimmed page behind the card closes it
      dialog.addEventListener('click', function (e) { if (e.target === dialog) closeSignIn(); });
      document.getElementById('signin-close').addEventListener('click', closeSignIn);
      document.getElementById('signin-guest').addEventListener('click', closeSignIn);
      document.getElementById('signin-retry').addEventListener('click', startUI);
      if (waitCancel) waitCancel.addEventListener('click', function () { setPending(false); startUI(); });
      document.getElementById('signin-to-signup').addEventListener('click', function () { setMode('signup'); });
      document.getElementById('signin-to-signin').addEventListener('click', function () { setMode('signin'); });
    }

    // all the auth handling is done in client side to improve performance(cache)
      // in stead of golang templates
      // install sign in button
    var signin_btn = document.getElementById('sign-in-button');
    var signup_btn = document.getElementById('sign-up-button');
    var signout_btn = document.getElementById('sign-out-button');

    // ?signin=1 or ?signup=1 opens the dialog for a visitor who is not signed in,
    // so another page can send people straight to it.
    function openFromUrl() {
      var params = new URLSearchParams(location.search);
      var want = params.get('signin') !== null ? 'signin' : (params.get('signup') !== null ? 'signup' : '');
      if (!want) return;
      params.delete('signin');
      params.delete('signup');
      var query = params.toString();
      history.replaceState(null, '', location.pathname + (query ? '?' + query : '') + location.hash);
      openSignIn(want);
    }

    // check if already logged in 
    var xhr = new XMLHttpRequest();
    var url = window.location.protocol + "//" + window.location.host + "/login";
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
                if (signup_btn) signup_btn.style.display="none";
                document.body.classList.add("is-signed-in");
                document.getElementById('user-account').style.display="inline-block";

                // render other profile data, take ur time
                setTimeout(renderProfileData, 2800);
            }

          } else {
            var resumed = takePending();
            if (resumed) openSignIn(resumed); // back from a provider's page; FirebaseUI finishes the sign-in
            else openFromUrl();
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
      signin_btn.addEventListener('click', function () { openSignIn('signin'); });
    }
    if (signup_btn) {
      signup_btn.addEventListener('click', function () { openSignIn('signup'); });
    }
    $('#guest-signup').on('click', function () { openSignIn('signup'); });
    // the nav links are role=button anchors; let Enter and Space open them too
    $('#sign-in-button, #sign-up-button').on('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openSignIn(this.id === 'sign-up-button' ? 'signup' : 'signin');
      }
    });

    if (signout_btn !== null && signout_btn !== undefined) {
      // install signout from firebase as well
      signout_btn.addEventListener('click', function() {
        firebase.auth().signOut();
      });
    }

    // Account menu: it opens on hover (CSS), and on click or Enter, which is
    // what touch screens and keyboards use. Escape, a click elsewhere or
    // moving focus away closes it.
    var account = document.getElementById('user-account');
    var accountButton = document.getElementById('user-account-button');
    if (account && accountButton) {
      var setAccountOpen = function (open) {
        account.classList.toggle('open', open);
        accountButton.setAttribute('aria-expanded', open ? 'true' : 'false');
      };
      accountButton.addEventListener('click', function () {
        setAccountOpen(!account.classList.contains('open'));
      });
      document.addEventListener('click', function (e) {
        if (!account.contains(e.target)) setAccountOpen(false);
      });
      account.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') { setAccountOpen(false); accountButton.focus(); }
      });
      account.addEventListener('focusout', function (e) {
        if (e.relatedTarget && !account.contains(e.relatedTarget)) setAccountOpen(false);
      });
    }
});

export {};  // an ES module: strict mode, bundled by webpack
