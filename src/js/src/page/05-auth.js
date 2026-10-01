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
    var uiConfig = {
      callbacks: {
        signInSuccessWithAuthResult: function(authResult, redirectUrl) {
          // User successfully signed in.
          // Return type determines whether we continue the redirect automatically
          // or whether we leave that to developer to handle.
          console.log("authResult: ",JSON.stringify(authResult), JSON.stringify(redirectUrl));

	  if ((authResult.user) && (authResult.user.emailVerified)) {
              // User is signed in and email is verified, so proceed with login
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
                          notify("Check your inbox and follow the link to verify your email address.", { type: "success", title: "Verification email sent" });
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
        firebase.auth.GithubAuthProvider.PROVIDER_ID,
        //firebase.auth.PhoneAuthProvider.PROVIDER_ID
      ],
      // Terms of service url.
      tosUrl: '/terms.html',
      // Privacy policy url.
      privacyPolicyUrl: '/privacy.html'
    };


    // all the auth handling is done in client side to improve performance(cache)
      // in stead of golang templates
      // install sign in button
    var signin_btn = document.getElementById('sign-in-button');
    var signup_btn = document.getElementById('sign-up-button');
    var signout_btn = document.getElementById('sign-out-button');

    function startauth() {
          // remove sign in button
          signin_btn.style.display="none";
          if (signup_btn) signup_btn.style.display="none";
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
          ensureAuthUI().then(function (authUI) {
            authUI.start('#firebaseui-auth-container', uiConfig);
          }).catch(function () {
            notify("Sign-in couldn't load. Check your connection and try again.", { type: "error" });
          });
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
    if (signup_btn) {
      signup_btn.addEventListener('click', startauth);
    }
    $('#guest-signup').on('click', startauth);
    // the nav links are role=button anchors; let Enter and Space open them too
    $('#sign-in-button, #sign-up-button').on('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); startauth(); }
    });

    if (signout_btn !== null && signout_btn !== undefined) {
      // install signout from firebase as well
      signout_btn.addEventListener('click', function() {
        firebase.auth().signOut();
      });
    }
});

export {};  // an ES module: strict mode, bundled by webpack
