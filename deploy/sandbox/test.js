'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

test('adds numbers', () => {
  assert.equal(1 + 1, 2);
});

test('joins strings', () => {
  assert.equal(['fu', 'garo'].join(''), 'fugaro');
});
