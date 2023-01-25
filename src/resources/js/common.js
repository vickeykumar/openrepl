// utilities
var get = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelector(selector);
};

var getAll = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelectorAll(selector);
};


(function commonApp() {
	// body...
	var topNav = get('.menu');
	var icon = get('.toggle');

	window.addEventListener('load', function(){
	    function showNav() {
	      if (topNav.className === 'menu') {
	        topNav.className += ' responsive';
	        icon.className += ' open';
	      } else {
	        topNav.className = 'menu';
	        icon.classList.remove('open');
	      }
	    }
	    icon.addEventListener('click', showNav);
	    //replace all urls with with origin url in case of iframe webredirect
	    var parent_origin = '';
	    if (document.referrer !== '') {
	    	parent_origin = new URL(document.referrer).origin;
	    }
	    if(window.self !== window.top && parent_origin !==window.location.origin) {
	    	//inside an iframe web redirect
	    	var links = document.links;
		let i = links.length;
		while (i--) {
			let absUrl = new URL(links[i].href, window.location.href).href;
			links[i].href = absUrl.replace(window.location.origin,parent_origin);
		}
	    }
	    
	});
})();
