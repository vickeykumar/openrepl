'use strict';

var Color = require('color');

// App to enable/disable console logs in production
(function () {
	try {
        if (typeof(window.console) != "undefined") {
        	window.ENABLE_LOGGING=new URL(location.href.toLowerCase()).searchParams.get("debug");
        	window.old_console_log = function () {};
            window.toggle_logging = function () {
            	if (window.ENABLE_LOGGING !== "") {
	            	if (typeof(window.old_console_log) != "undefined") {
	            		var temp_logger = window.old_console_log;
			            window.old_console_log = window.console.log;
			            window.console.log = temp_logger;
	            	}
	            }
            	
            }
            // now toggle logging if destination is not debug
            window.toggle_logging();     
        }

    } catch (ex) {
    	console.error("toggle_logging: threw an exception: ",ex);
    }
})();

// App to change color schemes daily
(function ColorSchemesApp(){
	var PopularColors = new Array(
		'#ffc0cb',
		'#008080',
		'#ff0000',
		'#ffd700',
		'#00ffff',
		'#40e0d0',
		'#ff7373',
		'#0000ff',
		'#ffa500',
		'#b0e0e6',
		'#7fffd4',
		'#c6e2ff',
		'#faebd7',
		'#800080',
		'#cccccc',
		'#fa8072',
		'#ffb6c1',
		'#333333',
		'#800000',
		'#00ff00',
		'#003366',
		'#c0c0c0',
		'#66cdaa',
		'#ff6666',
		'#666666',
		'#c39797',
		'#00ced1',
		'#ffdab9',
		'#ff00ff',
		'#008000',
		'#FE6A6B',
		'#088da5',
		'#c0d6e4',
		'#660066',
		'#0e2f44',
		'#808080',
		'#8b0000',
		'#ff7f50',
		'#990000',
		'#daa520',
		'#00ff7f',
		'#66cccc',
		'#8a2be2',
		'#81d8d0',
		'#3399ff',
		'#a0db8e',
		'#0bd800',
		'#ff4040',
		'#794044',
		'#cc0000',
		'#000080',
		'#3b5998',
		'#ccff00',
		'#999999',
		'#191970',
		'#31698a',
		'#6897bb',
		'#0099cc',
		'#ff4444',
		'#ff1493',
		'#6dc066',
	);

	function rand_from_seed(x, container_size, iterations){
	  iterations = iterations || 100;
	  container_size = container_size || 10000;
	  for(var i = 0; i < iterations; i++)
	    x = (x ^ (x << 1) ^ (x >> 1)) % container_size;
	  return Math.abs(x);
	}
	
	function randomNumber(minimum, maximum){
    	return Math.round((Math.random() * (maximum - minimum) + minimum) * 100)/100;
	}
	
	function ColorOfTheDay() {
		var ms = 1000*60*60*24;
		var seed = Math.floor(new Date().getTime()/ms);
		var noOftheDay = rand_from_seed(seed, PopularColors.length);
		return PopularColors[noOftheDay%PopularColors.length];
	}

	function adjustcolor(colorObj) {
		var minrand = 0;
		console.log('before lumin1: ',colorObj.luminosity());
		if(colorObj.isLight()) {
			console.log('light color: ',colorObj.hex());
			if (colorObj.luminosity() > 0.7) {
				minrand = 0.3;
			}
			colorObj = colorObj.darken(randomNumber(minrand,0.4));
		} else {
			console.log('dark color: ',colorObj.hex());
			if (colorObj.luminosity()<0.2) {
				minrand = 0.5;
			}
			colorObj = colorObj.lighten(randomNumber(minrand,0.7));
		}
		console.log('after lumin: ',colorObj.luminosity());
		return colorObj;
	}

	function setupColorThemes() {
		try {
			var accent_color = ColorOfTheDay();
			console.log('ColorOfTheDay: ',accent_color);
			if (accent_color!==undefined) {
				var colorObj = Color(accent_color);
				colorObj = adjustcolor(colorObj);
				var accent_color_light = colorObj.alpha(0.5).lighten(0.5);
				var accent_color_dark = colorObj.alpha(0.9).darken(0.5);
				var accent_color_rev = adjustcolor(colorObj.negate().alpha(0.5).darken(0.2));
				var accent_color_rev_light = accent_color_rev.alpha(0.5).lighten(0.5);
				var accent_color_rev_dark = accent_color_rev.alpha(0.9).darken(0.5);
				//console.log('color: ',colorObj);
				document.documentElement.style.setProperty('--accent-color', colorObj.hex());
				document.documentElement.style.setProperty('--rev-accent-color', accent_color_rev.hex());
				document.documentElement.style.setProperty('--accent-color-light', accent_color_light.hex());
				document.documentElement.style.setProperty('--accent-color-dark', accent_color_dark.hex());
				document.documentElement.style.setProperty('--rev-accent-color-dark', accent_color_rev_dark.hex());
				document.documentElement.style.setProperty('--rev-accent-color-light', accent_color_rev_light.hex());
			}
			// statements
		} catch(e) {
			// statements
			console.log("Unable to create color theme: ",e);
		}
	}

	setupColorThemes();

})();

