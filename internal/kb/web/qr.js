'use strict';
// Minimal QR Code encoder (ISO/IEC 18004) for the MFA setup screen.
// Byte mode, error correction level M, versions 1-40, automatic mask choice.
// It runs in the browser (same origin, so the CSP stays script-src 'self') and
// never sends the otpauth secret anywhere.

// Error correction codewords per block and block counts for level M, indexed by version.
const QR_ECC_PER_BLOCK=[-1,10,16,26,18,24,16,18,22,22,26,30,22,22,24,24,28,28,26,26,26,26,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28];
const QR_ECC_BLOCKS=[-1,1,1,1,2,2,4,4,4,5,5,5,8,9,9,10,10,11,13,14,16,17,17,18,20,21,23,25,26,28,29,31,33,35,37,38,40,43,45,47,49];
const QR_FORMAT_M=0;

function qrRawModules(ver){
  let n=(16*ver+128)*ver+64;
  if(ver>=2){const a=Math.floor(ver/7)+2;n-=(25*a-10)*a-55;if(ver>=7)n-=36;}
  return n;
}
function qrDataCodewords(ver){return Math.floor(qrRawModules(ver)/8)-QR_ECC_PER_BLOCK[ver]*QR_ECC_BLOCKS[ver];}

function qrMul(x,y){
  let z=0;
  for(let i=7;i>=0;i--){z=(z<<1)^((z>>>7)*0x11d);z^=((y>>>i)&1)*x;}
  return z&0xff;
}
function qrDivisor(degree){
  const r=new Array(degree).fill(0);r[degree-1]=1;
  let root=1;
  for(let i=0;i<degree;i++){
    for(let j=0;j<degree;j++){r[j]=qrMul(r[j],root);if(j+1<degree)r[j]^=r[j+1];}
    root=qrMul(root,2);
  }
  return r;
}
function qrRemainder(data,div){
  const r=new Array(div.length).fill(0);
  for(const b of data){
    const f=b^r.shift();r.push(0);
    div.forEach((c,i)=>{r[i]^=qrMul(c,f);});
  }
  return r;
}

// qrMatrix returns a square boolean matrix (true = dark) without the quiet zone.
function qrMatrix(text){
  const bytes=Array.from(new TextEncoder().encode(String(text)));
  let ver=1;
  for(;ver<=40;ver++){
    const countBits=ver<10?8:16;
    if(4+countBits+bytes.length*8<=qrDataCodewords(ver)*8)break;
  }
  if(ver>40)throw new Error('QR 코드에 넣기에는 내용이 너무 깁니다.');
  const capacity=qrDataCodewords(ver)*8,bits=[];
  const put=(v,n)=>{for(let i=n-1;i>=0;i--)bits.push((v>>>i)&1);};
  put(4,4);put(bytes.length,ver<10?8:16);
  for(const b of bytes)put(b,8);
  put(0,Math.min(4,capacity-bits.length));
  put(0,(8-bits.length%8)%8);
  for(let pad=0xec;bits.length<capacity;pad^=0xec^0x11)put(pad,8);
  const data=[];
  for(let i=0;i<bits.length;i+=8){let v=0;for(let j=0;j<8;j++)v=(v<<1)|bits[i+j];data.push(v);}

  // Split into blocks, add Reed-Solomon codewords, then interleave.
  const nBlocks=QR_ECC_BLOCKS[ver],eccLen=QR_ECC_PER_BLOCK[ver],raw=Math.floor(qrRawModules(ver)/8);
  const nShort=nBlocks-raw%nBlocks,shortLen=Math.floor(raw/nBlocks),div=qrDivisor(eccLen),blocks=[];
  for(let i=0,k=0;i<nBlocks;i++){
    const len=shortLen-eccLen+(i<nShort?0:1),dat=data.slice(k,k+len);k+=len;
    const ecc=qrRemainder(dat,div);
    if(i<nShort)dat.push(0);
    blocks.push(dat.concat(ecc));
  }
  const codewords=[];
  for(let i=0;i<blocks[0].length;i++)blocks.forEach((b,j)=>{if(i!==shortLen-eccLen||j>=nShort)codewords.push(b[i]);});

  const size=ver*4+17;
  const mod=Array.from({length:size},()=>new Array(size).fill(false));
  const fn=Array.from({length:size},()=>new Array(size).fill(false));
  const set=(x,y,dark)=>{mod[y][x]=dark;fn[y][x]=true;};

  for(let i=0;i<size;i++){set(6,i,i%2===0);set(i,6,i%2===0);}
  for(const [cx,cy] of [[3,3],[size-4,3],[3,size-4]]){
    for(let dy=-4;dy<=4;dy++)for(let dx=-4;dx<=4;dx++){
      const d=Math.max(Math.abs(dx),Math.abs(dy)),x=cx+dx,y=cy+dy;
      if(x>=0&&x<size&&y>=0&&y<size)set(x,y,d!==2&&d!==4);
    }
  }
  const align=[];
  if(ver>1){
    const n=Math.floor(ver/7)+2,step=Math.floor((ver*8+n*3+5)/(n*4-4))*2;
    align.push(6);
    for(let p=size-7;align.length<n;p-=step)align.splice(1,0,p);
  }
  align.forEach((ay,i)=>align.forEach((ax,j)=>{
    if((i===0&&j===0)||(i===0&&j===align.length-1)||(i===align.length-1&&j===0))return;
    for(let dy=-2;dy<=2;dy++)for(let dx=-2;dx<=2;dx++)set(ax+dx,ay+dy,Math.max(Math.abs(dx),Math.abs(dy))!==1);
  }));
  const drawFormat=mask=>{
    const d=(QR_FORMAT_M<<3)|mask;let r=d;
    for(let i=0;i<10;i++)r=(r<<1)^((r>>>9)*0x537);
    const b=((d<<10)|r)^0x5412,bit=i=>((b>>>i)&1)===1;
    for(let i=0;i<=5;i++)set(8,i,bit(i));
    set(8,7,bit(6));set(8,8,bit(7));set(7,8,bit(8));
    for(let i=9;i<15;i++)set(14-i,8,bit(i));
    for(let i=0;i<8;i++)set(size-1-i,8,bit(i));
    for(let i=8;i<15;i++)set(8,size-15+i,bit(i));
    set(8,size-8,true);
  };
  drawFormat(0);
  if(ver>=7){
    let r=ver;
    for(let i=0;i<12;i++)r=(r<<1)^((r>>>11)*0x1f25);
    const b=(ver<<12)|r;
    for(let i=0;i<18;i++){const dark=((b>>>i)&1)===1,a=size-11+i%3,c=Math.floor(i/3);set(a,c,dark);set(c,a,dark);}
  }

  // Place data bits in the zigzag order, skipping function modules.
  let i=0;
  for(let right=size-1;right>=1;right-=2){
    if(right===6)right=5;
    for(let v=0;v<size;v++)for(let j=0;j<2;j++){
      const x=right-j,y=((right+1)&2)===0?size-1-v:v;
      if(!fn[y][x]&&i<codewords.length*8){mod[y][x]=((codewords[i>>>3]>>>(7-(i&7)))&1)===1;i++;}
    }
  }

  const maskBit=(m,x,y)=>{
    switch(m){
      case 0:return(x+y)%2===0;
      case 1:return y%2===0;
      case 2:return x%3===0;
      case 3:return(x+y)%3===0;
      case 4:return(Math.floor(x/3)+Math.floor(y/2))%2===0;
      case 5:return x*y%2+x*y%3===0;
      case 6:return(x*y%2+x*y%3)%2===0;
      default:return((x+y)%2+x*y%3)%2===0;
    }
  };
  const applyMask=m=>{for(let y=0;y<size;y++)for(let x=0;x<size;x++)if(!fn[y][x]&&maskBit(m,x,y))mod[y][x]=!mod[y][x];};
  let best=0,bestScore=Infinity;
  for(let m=0;m<8;m++){
    applyMask(m);drawFormat(m);
    const s=qrPenalty(mod);
    if(s<bestScore){best=m;bestScore=s;}
    applyMask(m);
  }
  applyMask(best);drawFormat(best);
  return mod;
}

// qrPenalty scores a matrix with the four standard rules; lower reads better.
function qrPenalty(mod){
  const size=mod.length;let score=0,dark=0;
  const finder=[true,false,true,true,true,false,true];
  const line=get=>{
    let run=1;
    for(let i=1;i<=size;i++){
      if(i<size&&get(i)===get(i-1)){run++;continue;}
      if(run>=5)score+=run-2;
      run=1;
    }
    for(let i=0;i+7<=size;i++){
      if(!finder.every((v,k)=>get(i+k)===v))continue;
      const light=(a,b)=>{for(let k=a;k<b;k++)if(k>=0&&k<size&&get(k))return false;return true;};
      if(light(i-4,i)||light(i+7,i+11))score+=40;
    }
  };
  for(let y=0;y<size;y++)line(x=>mod[y][x]);
  for(let x=0;x<size;x++)line(y=>mod[y][x]);
  for(let y=0;y<size;y++)for(let x=0;x<size;x++){
    if(mod[y][x])dark++;
    if(x+1<size&&y+1<size){const c=mod[y][x];if(c===mod[y][x+1]&&c===mod[y+1][x]&&c===mod[y+1][x+1])score+=3;}
  }
  const total=size*size;
  score+=Math.max(0,Math.ceil(Math.abs(dark*20-total*10)/total)-1)*10;
  return score;
}

// qrSVG draws the matrix as an SVG with a 4-module quiet zone. It uses only
// attributes (no inline style), so the page CSP needs no change.
function qrSVG(text,label){
  const mod=qrMatrix(text),n=mod.length+8,ns='http://www.w3.org/2000/svg';
  const svg=document.createElementNS(ns,'svg');
  svg.setAttribute('viewBox','0 0 '+n+' '+n);
  svg.setAttribute('shape-rendering','crispEdges');
  svg.setAttribute('class','qr');
  svg.setAttribute('role','img');
  if(label)svg.setAttribute('aria-label',label);
  const bg=document.createElementNS(ns,'rect');
  bg.setAttribute('width',n);bg.setAttribute('height',n);bg.setAttribute('fill','#fff');
  let d='';
  mod.forEach((row,y)=>row.forEach((on,x)=>{if(on)d+='M'+(x+4)+' '+(y+4)+'h1v1h-1z';}));
  const path=document.createElementNS(ns,'path');
  path.setAttribute('d',d);path.setAttribute('fill','#000');
  svg.append(bg,path);
  return svg;
}

if(typeof module!=='undefined')module.exports={qrMatrix,qrPenalty};
