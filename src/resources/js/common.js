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
	});
})();