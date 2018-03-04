'use strict';

Object.defineProperty(exports, "__esModule", {
  value: true
});
exports.put = exports.get = undefined;

var _crypto = require('crypto');

var _crypto2 = _interopRequireDefault(_crypto);

var _cacache = require('cacache');

var _cacache2 = _interopRequireDefault(_cacache);

function _interopRequireDefault(obj) { return obj && obj.__esModule ? obj : { default: obj }; }

var sha512 = 'sha512';
var getHash = function getHash(data) {
  return `${sha512}-${_crypto2.default.createHash(sha512).update(data).digest('base64')}`;
};

var get = exports.get = function get(cacheDirectory, key, identifier) {
  return _cacache2.default.get(cacheDirectory, key).then(function (_ref) {
    var data = _ref.data,
        metadata = _ref.metadata;

    var hash = getHash(identifier);
    if (metadata.hash !== hash) {
      return Promise.reject(new Error('The cache has expired'));
    }
    return JSON.parse(data);
  });
};

var put = exports.put = function put(cacheDirectory, key, data, identifier) {
  var hash = getHash(identifier);
  return _cacache2.default.put(cacheDirectory, key, JSON.stringify(data), { metadata: { hash } });
};