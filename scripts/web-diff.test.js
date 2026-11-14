'use strict';
// Unit tests for the web console's revision diff (internal/kb/web/diff.js).
// Run: node --test scripts/
const test=require('node:test');
const assert=require('node:assert');
const {lineDiff,diffHunks,diffStats}=require('../internal/kb/web/diff.js');

// apply rebuilds both sides from the edit script; a correct diff reproduces them.
function sides(ops){return{before:ops.filter(o=>o.op!=='+').map(o=>o.text),after:ops.filter(o=>o.op!=='-').map(o=>o.text)};}

test('identical input has no edits',()=>{
  const ops=lineDiff(['a','b'],['a','b']);
  assert.deepStrictEqual(diffStats(ops),{added:0,removed:0});
  assert.strictEqual(diffHunks(ops).length,0);
});

test('insert, delete and replace reproduce both sides with minimal edits',()=>{
  const cases=[
    [[],['x']],[['x'],[]],[['a','b','c'],['a','x','c']],[['a','b','c','d'],['b','c','d','e']],
    [['a','b','c','a','b','b','a'],['c','b','a','b','a','c']],
  ];
  for (const [a,b] of cases) {
    const ops=lineDiff(a,b);
    assert.deepStrictEqual(sides(ops),{before:a,after:b});
  }
  // Classic Myers example: shortest edit script has 5 edits.
  const s=diffStats(lineDiff([...'ABCABBA'],[...'CBABAC']));
  assert.strictEqual(s.added+s.removed,5);
});

test('random inputs round-trip',()=>{
  let seed=7;const rnd=n=>{seed=(seed*1103515245+12345)%2147483648;return seed%n;};
  for (let i=0;i<300;i++) {
    const a=Array.from({length:rnd(30)},()=>'l'+rnd(5)), b=Array.from({length:rnd(30)},()=>'l'+rnd(5));
    assert.deepStrictEqual(sides(lineDiff(a,b)),{before:a,after:b});
  }
});

test('hunks keep three lines of context and number lines',()=>{
  const a=Array.from({length:20},(_,i)=>'line '+i), b=[...a];b[10]='changed';
  const hunks=diffHunks(lineDiff(a,b));
  assert.strictEqual(hunks.length,1);
  const lines=hunks[0].lines;
  assert.strictEqual(lines.length,8);
  assert.deepStrictEqual(lines.find(l=>l.op==='-'),{op:'-',text:'line 10',old:11,new:null});
  assert.deepStrictEqual(lines.find(l=>l.op==='+'),{op:'+',text:'changed',old:null,new:11});
});

test('very different large inputs fall back without hanging',()=>{
  const a=Array.from({length:4000},(_,i)=>'a'+i), b=Array.from({length:4000},(_,i)=>'b'+i);
  const ops=lineDiff(a,b);
  assert.deepStrictEqual(sides(ops),{before:a,after:b});
});
