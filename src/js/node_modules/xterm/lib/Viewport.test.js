"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
var chai_1 = require("chai");
var Viewport_1 = require("./Viewport");
describe('Viewport', function () {
    var terminal;
    var viewportElement;
    var selectionContainer;
    var charMeasure;
    var viewport;
    var scrollAreaElement;
    var CHARACTER_HEIGHT = 10;
    beforeEach(function () {
        terminal = {
            lines: [],
            rows: 0,
            ydisp: 0,
            on: function () { },
            rowContainer: {
                style: {
                    lineHeight: 0
                }
            },
            selectionContainer: {
                style: {
                    height: 0
                }
            }
        };
        viewportElement = {
            addEventListener: function () { },
            style: {
                height: 0,
                lineHeight: 0
            }
        };
        scrollAreaElement = {
            style: {
                height: 0
            }
        };
        charMeasure = {
            height: CHARACTER_HEIGHT
        };
        viewport = new Viewport_1.Viewport(terminal, viewportElement, scrollAreaElement, charMeasure);
    });
    describe('refresh', function () {
        it('should set the line-height of the terminal', function (done) {
            setTimeout(function () {
                chai_1.assert.equal(viewportElement.style.lineHeight, CHARACTER_HEIGHT + 'px');
                chai_1.assert.equal(terminal.rowContainer.style.lineHeight, CHARACTER_HEIGHT + 'px');
                charMeasure.height = 1;
                viewport.refresh();
                chai_1.assert.equal(viewportElement.style.lineHeight, '1px');
                chai_1.assert.equal(terminal.rowContainer.style.lineHeight, '1px');
                done();
            }, 0);
        });
        it('should set the height of the viewport when the line-height changed', function () {
            terminal.lines.push('');
            terminal.lines.push('');
            terminal.rows = 1;
            viewport.refresh();
            chai_1.assert.equal(viewportElement.style.height, 1 * CHARACTER_HEIGHT + 'px');
            charMeasure.height = 20;
            viewport.refresh();
            chai_1.assert.equal(viewportElement.style.height, 20 + 'px');
        });
    });
    describe('syncScrollArea', function () {
        it('should sync the scroll area', function (done) {
            setTimeout(function () {
                terminal.lines.push('');
                terminal.rows = 1;
                chai_1.assert.equal(scrollAreaElement.style.height, 0 * CHARACTER_HEIGHT + 'px');
                viewport.syncScrollArea();
                chai_1.assert.equal(viewportElement.style.height, 1 * CHARACTER_HEIGHT + 'px');
                chai_1.assert.equal(scrollAreaElement.style.height, 1 * CHARACTER_HEIGHT + 'px');
                terminal.lines.push('');
                viewport.syncScrollArea();
                chai_1.assert.equal(viewportElement.style.height, 1 * CHARACTER_HEIGHT + 'px');
                chai_1.assert.equal(scrollAreaElement.style.height, 2 * CHARACTER_HEIGHT + 'px');
                done();
            }, 0);
        });
    });
});

//# sourceMappingURL=Viewport.test.js.map
