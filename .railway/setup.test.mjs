import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,readFileSync,rmSync,existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const script=fileURLToPath(new URL('./setup.mjs',import.meta.url));
test('prepare an isolated install without overwriting existing configuration',()=>{
  const dir=mkdtempSync(join(tmpdir(),'kb-setup-'));
  try{
    const path=join(dir,'deployment.local.json');
    const args=[script,'--project','new-team','--repo','team/knowledge','--output',path];
    assert.equal(spawnSync(process.execPath,args).status,0);
    const content=readFileSync(path,'utf8'),config=JSON.parse(content);
    assert.deepEqual(Object.keys(config.environments),['production']);
    assert.equal(config.environments.production.attachments.name,'Attachments');
    assert.equal(spawnSync(process.execPath,args).status,1);
    assert.equal(readFileSync(path,'utf8'),content);
    assert.equal(spawnSync(process.execPath,[...args,'--environment','development','--output',join(dir,'invalid.json')]).status,1);
    assert.equal(existsSync(join(dir,'invalid.json')),false);
  }finally{rmSync(dir,{recursive:true,force:true});}
});
