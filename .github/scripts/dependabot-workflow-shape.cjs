'use strict'
// YAML permits either indentation/chomping indicator order. A mapping after
// a sequence dash starts at the key column, not at the dash's indentation.
function blockScalarIndent(line) {
  const match=line.match(/^([ ]*)(-[ \t]+)?[^#]+:[ \t]*[|>](?:[1-9][+-]?|[+-][1-9]?)?[ \t]*(?:#.*)?$/)
  return match?match[1].length+(match[2]?.length || 0):null
}
// Expand flow collections as data, never as code. Keep scalar spelling and
// mapping order so protected workflow values remain part of scope comparison.
function expandWorkflowFlow(content) {
  const lines=content.split(/\r?\n/),out=[],offsets=[];let scalarIndent=null,changed=false,offset=0
  for(const line of lines){offsets.push(offset);offset+=line.length+1}
  const sourceText=lines.join('\n')
  function parse(source) {
    let pos=0,nodes=0;const protectedComments=[]
    const comment=(node,location,inlineOnly=false)=>{
      const begin=pos
      while(pos<source.length) {
        if(/\s/.test(source[pos])){if(inlineOnly && source[pos]==='\n')break;pos++;continue}
        if(source[pos]!=='#')break
        const end=source.indexOf('\n',pos),stop=end<0?source.length:end
        const text=source.slice(pos+1,stop).trim()
        const annotates=node && (node.kind==='scalar' || (node.kind==='map' && node.items.some(item=>item.key.replace(/^['"]|['"]$/g,'')==='uses' && item.value.kind==='scalar' && !item.value.comment)))
        const version=annotates && !node.comment && /^v\d+(?:\.\d+){0,2}$/.test(text)
        if(version)node.comment=text
        // Keep structural ownership and separator position, not just comment
        // text. Only rendered version annotations can mask their version.
        protectedComments.push([location,source.slice(begin,pos).includes('\n'),version?'version':'literal',version?source.slice(pos,stop).replace(text,'VERSION_TAG'):source.slice(pos,stop)])
        pos=stop
      }
    }
    const quoted=()=>{
      const start=pos,quote=source[pos++]
      while(pos<source.length){
        if(source[pos]===quote){pos++;if(quote==="'"&&source[pos]===quote){pos++;continue}return source.slice(start,pos)}
        if(quote==='"'&&source[pos]==='\\')pos++
        pos++
      }
      throw Error('Unterminated flow scalar')
    }
    function value(depth,location) {
      if(depth>32 || ++nodes>10000)throw Error('Workflow flow collection limit')
      comment(null,[...location,'before']);const start=pos,ch=source[pos]
      if(ch==='{' || ch==='[') {
        pos++;const map=ch==='{',end=map?'}':']',items=[],keys=new Set();comment(null,[...location,'open'])
        while(source[pos]!==end) {
          if(pos>=source.length)throw Error('Unterminated workflow flow collection')
          let key
          if(map) {
            const begin=pos
            if(source[pos]==='"'||source[pos]==="'")key=quoted()
            else{while(pos<source.length && !/[:,{}\[\]\n]/.test(source[pos]))pos++;key=source.slice(begin,pos).trim()}
            if(!/^(?:[a-zA-Z_][\w-]*|"[a-zA-Z_][\w-]*"|'[a-zA-Z_][\w-]*')$/.test(key))throw Error('Unsupported flow mapping key')
            comment(null,[...location,items.length,'key']);if(source[pos++]!==':')throw Error('Missing flow mapping colon')
            const identity=key.replace(/^['"]|['"]$/g,'')
            if(keys.has(identity))throw Error('Duplicate flow mapping key');keys.add(identity)
          }
          const childLocation=[...location,items.length],child=value(depth+1,childLocation);items.push(map?{key,value:child}:child);comment(child,[...childLocation,'value'])
          if(source[pos]===end)break
          if(source[pos++]!==',')throw Error('Missing flow collection separator')
          comment(child,[...childLocation,'separator'])
        }
        pos++;return {kind:map?'map':'seq',items}
      }
      let raw
      if(ch==='"'||ch==="'")raw=quoted()
      else {while(pos<source.length && !/[,{}\[\]\n]/.test(source[pos]) && !(source[pos]==='#' && /\s/.test(source[pos-1] || ' ')))pos++;raw=source.slice(start,pos).trim()}
      if(!raw)throw Error('Empty or unsupported flow scalar')
      return {kind:'scalar',raw}
    }
    const node=value(0,[]),end=pos
    // A comment after a flow step/job conventionally annotates its uses value.
    while(source[pos]===' ' || source[pos]==='\t')pos++
    if(source[pos]==='#')comment(node,['end'],true)
    return {node,end,protectedComments}
  }
  function render(node,indent,inherited) {
    const space=' '.repeat(indent)
    if(node.kind==='scalar')return [space+node.raw+(node.comment?' # '+node.comment:'')]
    if(!node.items.length)return [space+(node.kind==='map'?'{}':'[]')]
    const result=[]
    if(node.kind==='map')for(const {key,value} of node.items){
      const tag=key.replace(/^['"]|['"]$/g,'')==='uses'?(value.comment || node.comment || inherited):value.comment
      if(value.kind==='scalar')result.push(space+key+': '+value.raw+(tag?' # '+tag:''))
      else{result.push(space+key+':');result.push(...render(value,indent+2))}
    }
    else for(const child of node.items){
      const rendered=render(child,indent+2,child.comment)
      result.push(space+'- '+rendered[0].slice(indent+2),...rendered.slice(1))
    }
    return result
  }
  for(let i=0;i<lines.length;i++){
    const line=lines[i],indent=line.match(/^ */)[0].length
    if(scalarIndent!==null && (!line.trim() || indent>scalarIndent)){out.push(line);continue}
    scalarIndent=null
    scalarIndent=blockScalarIndent(line)
    const match=line.match(/^([ ]*)(-\s+|(?:"[\w-]+"|'[\w-]+'|[\w-]+):[ \t]*)([\[{].*)$/)
    if(!match){out.push(line);continue}
    const source=sourceText.slice(offsets[i]+line.length-match[3].length),{node,end,protectedComments}=parse(source)
    const consumed=source.slice(0,end).split('\n').length-1
    const tail=source.slice(end).split('\n')[0]
    if(tail.trim() && !tail.trimStart().startsWith('#'))throw Error('Unsupported trailing flow content')
    if(match[2].trim()==='-'){
      const rendered=render(node,indent+2,node.comment)
      out.push(' '.repeat(indent)+'- '+rendered[0].slice(indent+2),...rendered.slice(1))
    } else {
      out.push(match[1]+match[2].trimEnd());out.push(...render(node,indent+2,node.comment))
    }
    // These internal comparison records cannot be forged by file bytes: the
    // content boundary rejects NUL. The workflow scanner treats them as comments.
    out.push(...protectedComments.map(comment=>match[1]+'#\0flow-comment '+JSON.stringify(comment)))
    i+=consumed;changed=true
  }
  return changed?out.join('\n'):content
}
module.exports={expandWorkflowFlow,blockScalarIndent}
