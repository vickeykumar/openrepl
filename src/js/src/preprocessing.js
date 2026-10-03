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
				// Button text on the accent: dark or white, whichever has more contrast today.
				var on_accent = colorObj.contrast(Color('#15151C')) >= colorObj.contrast(Color('#FFFFFF')) ? '#15151C' : '#FFFFFF';
				document.documentElement.style.setProperty('--on-accent-color', on_accent);
			}
			// statements
		} catch(e) {
			// statements
			console.log("Unable to create color theme: ",e);
		}
	}

	// Colour of the day is off by default. An admin can turn it on at /admin,
	// which sets site_settings.colorOfTheDay (served by /settings.js).
	if (window.site_settings && window.site_settings.colorOfTheDay) {
		setupColorThemes();
	}

})();


// App to show what an admin set at /admin (served by /settings.js): the
// announcement and maintenance banners, languages that are switched off, and
// the Genie switch. The texts come from the admin but are still put on the
// page as text only.
(function SiteNoticesApp(){
	var s = window.site_settings;
	if (!s) return;

	var CSS = '.site-banner{display:flex;align-items:center;gap:12px;padding:9px 16px;font:500 14px/1.4 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:#fff;background:#1d3b66}' +
		'.site-banner--warning{color:#15151C;background:#F5C37A}' +
		'.site-banner--maintenance{background:#8f2d2d}' +
		'.site-banner__text{flex:1;text-align:center;overflow-wrap:anywhere}' +
		'.site-banner__close{flex:none;width:28px;height:28px;border:0;border-radius:6px;background:transparent;color:inherit;font-size:20px;line-height:1;cursor:pointer}' +
		'.site-banner__close:hover,.site-banner__close:focus-visible{background:rgba(255,255,255,.2)}';

	function key(text) {
		var h = 0;
		for (var i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) | 0;
		return 'announcement-dismissed-' + h;
	}
	function dismissed(text) {
		try { return localStorage.getItem(key(text)) === '1'; } catch (e) { return false; }
	}
	function dismiss(text) {
		try { localStorage.setItem(key(text), '1'); } catch (e) {}
	}

	function banner(kind, text, canDismiss, onDismiss) {
		var el = document.createElement('div');
		el.className = 'site-banner site-banner--' + kind;
		el.setAttribute('role', kind === 'announcement' ? 'status' : 'alert');
		var span = document.createElement('span');
		span.className = 'site-banner__text';
		span.textContent = text;
		el.appendChild(span);
		if (canDismiss) {
			var b = document.createElement('button');
			b.type = 'button';
			b.className = 'site-banner__close';
			b.setAttribute('aria-label', 'Dismiss this message');
			b.textContent = '×';
			b.addEventListener('click', function () { el.remove(); onDismiss(); });
			el.appendChild(b);
		}
		return el;
	}

	function apply() {
		var banners = [];
		var m = s.maintenance;
		if (m && m.enabled) {
			banners.push(banner('maintenance', m.message || 'The site is down for maintenance. Please try again soon.', false));
		}
		var a = s.announcement;
		if (a && a.text && !dismissed(a.text)) {
			banners.push(banner(a.level === 'warning' ? 'warning' : 'info', a.text, true, function () { dismiss(a.text); }));
		}
		if (banners.length) {
			var style = document.createElement('style');
			style.textContent = CSS;
			document.head.appendChild(style);
			banners.reverse().forEach(function (b) { document.body.insertBefore(b, document.body.firstChild); });
		}

		// Languages that are switched off stay in the picker but cannot be chosen.
		var off = s.disabledLanguages || [];
		var list = document.getElementById('optionlist');
		if (list && off.length) {
			Array.prototype.forEach.call(list.options, function (o) {
				if (off.indexOf(o.value) >= 0) {
					o.disabled = true;
					o.textContent = o.textContent + ' (off)';
				}
			});
		}

		// Genie is switched off: no button leads to it.
		if (s.genieDisabled) {
			Array.prototype.forEach.call(document.querySelectorAll('#genie-button, [data-chat-widget-button]'), function (b) {
				b.hidden = true;
				b.style.display = 'none';
			});
		}
	}

	if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', apply);
	else apply();
})();
