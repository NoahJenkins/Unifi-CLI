'use strict'
const fs=require('node:fs');const path=require('node:path');const {randomBytes}=require('node:crypto');const {execFileSync}=require('node:child_process')
const {validateArtifact,validateFiles,sealArtifact,scopeChanges}=require('./dependabot-opencode-contract.cjs')
const {evaluateRepairScope}=require('./dependabot-controller-policy.cjs')
const PREPARE_COMMAND='go mod tidy'
const GENERATED=['go.mod','go.sum']
const COMMAND='git init -q && git add . && git -c user.name=Validator -c user.email=validator@example.invalid commit -qm snapshot && go run ./cmd/release-smoke --all && bash ./scripts/smoke.sh && go test -race ./... -timeout 20m && bash ./scripts/check-coverage.sh && go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...'

function rejectLinks(root) {
 for(const name of fs.readdirSync(root)) {const p=path.join(root,name);const stat=fs.lstatSync(p);if(stat.isSymbolicLink())throw Error('Symlink validation input');if(stat.isDirectory() && !['node_modules','.next','.git'].includes(name))rejectLinks(p)}
}
function validationArgs(root,image,name,command=COMMAND) {return ['run',...(name?['--name',name]:[]),'--rm','--platform','linux/amd64','--cap-drop=ALL','--security-opt=no-new-privileges','--user','1000:1000','--memory=6g','--cpus=2','--pids-limit=256','--tmpfs','/tmp:rw,exec,nosuid,nodev,size=2g','-e','HOME=/tmp/home','-e','CI=true','-e','GOPATH=/tmp/go','-e','GIT_CONFIG_COUNT=1','-e','GIT_CONFIG_KEY_0=safe.directory','-e','GIT_CONFIG_VALUE_0=/workspace','-v',`${root}:/workspace:rw`,'-w','/workspace',image,'sh','-eu','-c',command]}
async function validateRepair(task,snapshot,artifact,options) {
 validateArtifact(artifact,{task,origin:options.origin,changedFiles:snapshot.changedFiles})
 if(task.kind==='review')return {artifact,checks:{inputDigest:artifact.digest,outputDigest:artifact.digest,success:true}}
 const files=artifact.result.files
 const changes=scopeChanges(files,snapshot,evaluateRepairScope)
 const scope=evaluateRepairScope(changes);if(!scope.allowed)throw Error(scope.reason)
 if(!options.root || !options.image)throw Error('Validation container input is missing')
 rejectLinks(options.root)
 for(const file of files) {const target=path.join(options.root,file.path);fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,file.content)}
 const deadline=Date.now()+1200000
 const execute=options.execute || ((args)=>execFileSync('docker',args,{env:{PATH:process.env.PATH},timeout:Math.max(1,deadline-Date.now()),stdio:['ignore','pipe','pipe']}))
 const name=`dependabot-validation-${randomBytes(8).toString('hex')}`
 const cleanup=options.cleanup || (container=>{try{execFileSync('docker',['rm','-f',container],{env:{PATH:process.env.PATH},stdio:'ignore'})}catch{}})
 const candidateRoot=fs.realpathSync(options.root)
 function readFile(p) {
   let current=candidateRoot
   for(const component of p.split('/')){current=path.join(current,component);const stat=fs.lstatSync(current);if(stat.isSymbolicLink())throw Error('Symlink validation output')}
   const resolved=fs.realpathSync(current);const stat=fs.lstatSync(current)
   if(!resolved.startsWith(`${candidateRoot}${path.sep}`) || !stat.isFile() || stat.mode&0o111 || stat.size>2*1024*1024)throw Error('Unsupported validation output')
   const bytes=fs.readFileSync(current), content=bytes.toString('utf8')
   if(!Buffer.from(content).equals(bytes))throw Error('Invalid UTF-8 validation output')
   return {path:p,content}
 }
 function unchanged(expected,phase) {
   for(const file of expected)if(readFile(file.path).content!==file.content)throw Error(`File changed during ${phase}: ${file.path}`)
 }
 async function run(command) {
   if(Date.now()>=deadline)throw Error('Validation time limit exceeded')
   try {await execute(validationArgs(options.root,options.image,name,command))}finally{cleanup(name)}
 }
 // Resolve generated dependencies first, then end that container before taking
 // a host-owned baseline. Tests/builds may not change any bytes we will publish.
 await run(PREPARE_COMMAND)
 unchanged(files.filter(f=>!GENERATED.includes(f.path)),'dependency generation')
 const paths=[...new Set([...files.map(f=>f.path),...GENERATED])]
 const baseline=paths.map(readFile)
 // An unchanged generated file is validation evidence, not an extra repair entry.
 const submitted=new Set(files.map(f=>f.path))
 validateFiles(baseline.filter(f=>submitted.has(f.path)))
 const generated=baseline.filter(f=>!submitted.has(f.path))
 if(generated.length)validateFiles(generated)
 await run(COMMAND)
 unchanged(baseline,'validation')
 const validated=baseline.filter(f=>f.content!==(snapshot.headFiles[f.path]??snapshot.contextFiles?.[f.path]))
 validateFiles(validated)
 const final=sealArtifact({...artifact,result:{files:validated}})
 return {artifact:final,checks:{inputDigest:artifact.digest,outputDigest:final.digest,success:true}}
}
module.exports={COMMAND,PREPARE_COMMAND,validationArgs,validateRepair,rejectLinks}
