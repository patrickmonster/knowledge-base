import {writeFileSync} from 'node:fs';
import {parseArgs} from 'node:util';
import {fileURLToPath} from 'node:url';

// Local preparation only: no Railway API calls, secret collection or deployment.
try {
  const {values:v}=parseArgs({options:{project:{type:'string'},repo:{type:'string'},environment:{type:'string',default:'production'},region:{type:'string',default:'sjc'},output:{type:'string'},help:{type:'boolean'}}});
  if(v.help){console.log('npm run setup -- --project YOUR-PROJECT --repo OWNER/REPO [--environment production] [--region sjc|iad|ams|sin]');process.exit(0);}
  if(!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(v.project??''))throw Error('--project must be a project name (letters, numbers, hyphen or underscore).');
  if(!/^[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_.-]+$/.test(v.repo??''))throw Error('--repo must be OWNER/REPOSITORY.');
  if(!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(v.environment)||v.environment==='development')throw Error('Choose an environment name other than the retired development environment.');
  if(!['sjc','iad','ams','sin'].includes(v.region))throw Error('Invalid bucket region.');
  const configuration={projectName:v.project,repository:v.repo,environments:{[v.environment]:{
    app:{name:'knowledge-base',branch:'main'},database:{name:'Postgres'},ollama:{name:'Ollama',volumeName:'ollama-models'},attachments:{name:'Attachments',region:v.region}
  }}};
  const output=v.output??fileURLToPath(new URL('./deployment.local.json',import.meta.url));
  writeFileSync(output,JSON.stringify(configuration,null,2)+'\n',{flag:'wx',mode:0o600});
  console.log('Created local deployment configuration. No Railway resources were created.');
  console.log('When ready: railway login → railway link → railway environment '+v.environment+' → railway config plan. Review the target and plan before railway config apply.');
} catch(e){console.error(e.code==='EEXIST'?'Configuration already exists; it was not overwritten. Use a fresh checkout or --output for a separate local file.':e.message);process.exitCode=1;}
