'use strict';

Object.defineProperty(exports, "__esModule", {
  value: true
});

var _createClass = function () { function defineProperties(target, props) { for (var i = 0; i < props.length; i++) { var descriptor = props[i]; descriptor.enumerable = descriptor.enumerable || false; descriptor.configurable = true; if ("value" in descriptor) descriptor.writable = true; Object.defineProperty(target, descriptor.key, descriptor); } } return function (Constructor, protoProps, staticProps) { if (protoProps) defineProperties(Constructor.prototype, protoProps); if (staticProps) defineProperties(Constructor, staticProps); return Constructor; }; }();

var _os = require('os');

var _os2 = _interopRequireDefault(_os);

var _findCacheDir = require('find-cache-dir');

var _findCacheDir2 = _interopRequireDefault(_findCacheDir);

var _workerFarm = require('worker-farm');

var _workerFarm2 = _interopRequireDefault(_workerFarm);

var _minify = require('./minify');

var _minify2 = _interopRequireDefault(_minify);

var _cache = require('./cache');

var _serialization = require('./serialization');

var _versions = require('./versions');

var _versions2 = _interopRequireDefault(_versions);

function _interopRequireDefault(obj) { return obj && obj.__esModule ? obj : { default: obj }; }

function _classCallCheck(instance, Constructor) { if (!(instance instanceof Constructor)) { throw new TypeError("Cannot call a class as a function"); } }

var workerFile = require.resolve('./worker');

try {
  // run test
  workerFile = require.resolve('../../dist/uglify/worker');
} catch (e) {} // eslint-disable-line no-empty

var _class = function () {
  function _class() {
    var parallel = arguments.length > 0 && arguments[0] !== undefined ? arguments[0] : {};

    _classCallCheck(this, _class);

    var options = parallel;
    if (typeof parallel === 'boolean') {
      options = { cache: parallel, workers: parallel };
    }
    var _options = options,
        cache = _options.cache,
        workers = _options.workers;

    this.cache = cache === true ? (0, _findCacheDir2.default)({ name: 'uglifyjs-webpack-plugin' }) : cache;
    this.workers = workers === true ? _os2.default.cpus().length - 1 : Math.min(Number(workers) || 0, _os2.default.cpus().length - 1);
  }

  _createClass(_class, [{
    key: 'worker',
    value: function worker(options, callback) {
      var _this = this;

      if (this.workers > 0) {
        this.workerFarm = (0, _workerFarm2.default)({
          maxConcurrentWorkers: this.workers
        }, workerFile);
        this.worker = function (opt, cb) {
          return _this.workerFarm(JSON.stringify(opt, _serialization.encode), cb);
        };
      } else {
        this.worker = function (opt, cb) {
          try {
            var result = (0, _minify2.default)(opt);
            cb(null, result);
          } catch (errors) {
            cb(errors);
          }
        };
      }

      this.worker(options, callback);
    }
  }, {
    key: 'exit',
    value: function exit() {
      if (this.workerFarm) {
        _workerFarm2.default.end(this.workerFarm);
      }
    }
  }, {
    key: 'runTasks',
    value: function runTasks(tasks, callback) {
      var _this2 = this;

      if (!tasks.length) {
        callback(null, []);
        return;
      }

      var toRun = tasks.length;
      var results = [];
      var step = function step(index, data) {
        toRun -= 1;
        results[index] = data;
        if (!toRun) {
          callback(null, results);
        }
      };

      tasks.forEach(function (task, index) {
        var cacheIdentifier = `${_versions2.default.uglify}|${_versions2.default.plugin}|${task.input}`;
        var enqueue = function enqueue() {
          _this2.worker(task, function (error, data) {
            var result = error ? { error } : data;
            var done = function done() {
              return step(index, result);
            };
            if (_this2.cache && !result.error) {
              (0, _cache.put)(_this2.cache, task.cacheKey, data, cacheIdentifier).then(done, done);
            } else {
              done();
            }
          });
        };
        if (_this2.cache) {
          (0, _cache.get)(_this2.cache, task.cacheKey, cacheIdentifier).then(function (data) {
            return step(index, data);
          }, enqueue);
        } else {
          enqueue();
        }
      });
    }
  }]);

  return _class;
}();

exports.default = _class;