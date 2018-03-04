"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
var chai_1 = require("chai");
var InputHandler_1 = require("./InputHandler");
describe('InputHandler', function () {
    describe('setCursorStyle', function () {
        it('should call Terminal.setOption with correct params', function () {
            var options = {};
            var terminal = {
                setOption: function (option, value) { return options[option] = value; }
            };
            var inputHandler = new InputHandler_1.InputHandler(terminal);
            inputHandler.setCursorStyle([0]);
            chai_1.assert.equal(options['cursorStyle'], 'block');
            chai_1.assert.equal(options['cursorBlink'], true);
            options = {};
            inputHandler.setCursorStyle([1]);
            chai_1.assert.equal(options['cursorStyle'], 'block');
            chai_1.assert.equal(options['cursorBlink'], true);
            options = {};
            inputHandler.setCursorStyle([2]);
            chai_1.assert.equal(options['cursorStyle'], 'block');
            chai_1.assert.equal(options['cursorBlink'], false);
            options = {};
            inputHandler.setCursorStyle([3]);
            chai_1.assert.equal(options['cursorStyle'], 'underline');
            chai_1.assert.equal(options['cursorBlink'], true);
            options = {};
            inputHandler.setCursorStyle([4]);
            chai_1.assert.equal(options['cursorStyle'], 'underline');
            chai_1.assert.equal(options['cursorBlink'], false);
            options = {};
            inputHandler.setCursorStyle([5]);
            chai_1.assert.equal(options['cursorStyle'], 'bar');
            chai_1.assert.equal(options['cursorBlink'], true);
            options = {};
            inputHandler.setCursorStyle([6]);
            chai_1.assert.equal(options['cursorStyle'], 'bar');
            chai_1.assert.equal(options['cursorBlink'], false);
        });
    });
});

//# sourceMappingURL=InputHandler.test.js.map
