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

    function setAttr(name, value) { if (dialog) dialog.setAttribute('data-' + name, value); }
    function setState(state, text) {
      clearTimeout(loadTimer);
      if (loadingText) loadingText.textContent = text || 'Loading sign-in…';
      setAttr('state', state);
      // never leave the dialog loading for ever
      if (state === 'loading') loadTimer = setTimeout(function () { setAttr('state', 'error'); }, 20000);
    }
    function setMode(next) {
      mode = MODES[next] ? next : 'signin';
      setAttr('mode', mode);
      document.getElementById('signin-title').textContent = MODES[mode].title;
      document.getElementById('signin-lead').textContent = MODES[mode].lead;
    }

    // Once signed in with Firebase, the site makes its own session from the result.
    function completeLogin(authResult) {
      setState('loading', 'Signing you in…');
      var xhr = new XMLHttpRequest();
      var url = window.location.protocol + "//" + window.location.host + "/login";
      xhr.open("POST", url, true);
      xhr.setRequestHeader("Content-Type", "application/json");
      xhr.onreadystatechange = function () {
          if (xhr.readyState === 4) {
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
          console.log("authResult: ",JSON.stringify(authResult), JSON.stringify(redirectUrl));
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
    if (container && window.MutationObserver) {
      new MutationObserver(function () {
        if (dialog.getAttribute('data-step') === 'verify') return;
        if (!container.querySelector('.firebaseui-container')) return;
        setAttr('step', container.querySelector('.firebaseui-idp-list') ? 'pick' : 'flow');
      }).observe(container, { childList: true, subtree: true });
    }

    // FirebaseUI forgets what it showed; a failure here must not stop the next start
    function resetUI() {
      try { if (ui) ui.reset(); } catch (e) { console.log("firebaseui reset: ", e); }
    }

    function startUI() {
      setAttr('step', 'pick');
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
            openFromUrl();
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
