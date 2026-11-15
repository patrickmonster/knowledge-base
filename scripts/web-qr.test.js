'use strict';
// Unit tests for the MFA setup QR encoder (internal/kb/web/qr.js).
// Run: node --test scripts/
const test=require('node:test');
const assert=require('node:assert');
const {qrMatrix,qrPenalty}=require('../internal/kb/web/qr.js');

const uri='otpauth://totp/Knowledge%20Base:someone%40example.com?algorithm=SHA1&digits=6&issuer=Knowledge+Base&period=30&secret=ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
const version=m=>(m.length-17)/4;

// readFormat reads the first format-information copy and checks its BCH code.
function readFormat(m){
  const at=(x,y)=>m[y][x]?1:0,bits=[];
  for(let i=0;i<=5;i++)bits.push(at(8,i));
  bits.push(at(8,7),at(8,8),at(7,8));
  for(let i=9;i<15;i++)bits.push(at(14-i,8));
  const word=bits.reduce((v,b,i)=>v|(b<<i),0)^0x5412,data=word>>>10;
  let r=data;
  for(let i=0;i<10;i++)r=(r<<1)^((r>>>9)*0x537);
  return{ecl:data>>>3,mask:data&7,valid:((data<<10)|r)===word};
}
function readFormatCopy2(m){
  const n=m.length,at=(x,y)=>m[y][x]?1:0,bits=[];
  for(let i=0;i<8;i++)bits.push(at(n-1-i,8));
  for(let i=8;i<15;i++)bits.push(at(8,n-15+i));
  return bits.reduce((v,b,i)=>v|(b<<i),0)^0x5412;
}

test('chooses the smallest version that fits',()=>{
  assert.strictEqual(version(qrMatrix('a')),1);
  assert.strictEqual(version(qrMatrix('x'.repeat(14))),1); // version 1-M holds 14 bytes
  assert.strictEqual(version(qrMatrix('x'.repeat(15))),2);
  assert.strictEqual(version(qrMatrix(uri)),8);
  assert.strictEqual(version(qrMatrix('x'.repeat(2331))),40); // version 40-M holds 2331 bytes
  assert.throws(()=>qrMatrix('x'.repeat(2332)),/너무 깁니다/);
});

test('draws finder, timing and dark modules',()=>{
  const m=qrMatrix(uri),n=m.length;
  for(const [ox,oy] of [[0,0],[n-7,0],[0,n-7]]){
    for(let y=0;y<7;y++)for(let x=0;x<7;x++){
      const d=Math.max(Math.abs(x-3),Math.abs(y-3));
      assert.strictEqual(m[oy+y][ox+x],d!==2,`finder at ${ox},${oy}`);
    }
  }
  for(let i=8;i<n-8;i++){assert.strictEqual(m[6][i],i%2===0);assert.strictEqual(m[i][6],i%2===0);}
  assert.strictEqual(m[n-8][8],true);
});

test('format information is level M with a valid BCH code in both copies',()=>{
  for(const text of ['a',uri,'한글 텍스트','x'.repeat(500)]){
    const m=qrMatrix(text),f=readFormat(m);
    assert.ok(f.valid,'BCH');
    assert.strictEqual(f.ecl,0);
    assert.ok(f.mask>=0&&f.mask<8);
    let r=(f.ecl<<3)|f.mask,d=r;
    for(let i=0;i<10;i++)r=(r<<1)^((r>>>9)*0x537);
    assert.strictEqual(readFormatCopy2(m),(d<<10)|r);
  }
});

test('is deterministic and picks the lowest-penalty mask',()=>{
  const a=qrMatrix(uri),b=qrMatrix(uri);
  assert.deepStrictEqual(a,b);
  assert.ok(Number.isFinite(qrPenalty(a)));
});
