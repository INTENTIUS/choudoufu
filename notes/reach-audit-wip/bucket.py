import json,os,re,sys,collections
SRC='/private/tmp/claude-501/-Users-alex-Documents-checkouts-intentius-choudoufu/e29f3221-b09d-4226-9252-d8925e09dffa/scratchpad/src/azurerm'
d=json.load(open(sys.argv[1]))
rows=[x for x in d['types'] if x.get('route')=='unmapped']
texts={}
for root,_,fs in os.walk(SRC+'/internal'):
    for f in fs:
        if f.endswith('.go') and not f.endswith('_test.go'):
            p=os.path.join(root,f); texts[p]=open(p,errors='replace').read()
funcs={}
for p,t in texts.items():
    for m in re.finditer(r'^func (\w+)\(\)',t,re.M): funcs.setdefault(m.group(1),p)
def resfile(t):
    for p,txt in texts.items():
        if p.endswith('registration.go'):
            m=re.search(r'"%s":\s*(\w+)\(\)'%re.escape(t),txt)
            if m and m.group(1) in funcs: return funcs[m.group(1)]
    cands=[p for p,txt in texts.items() if ('return "%s"'%t) in txt and not p.endswith('data_source.go')]
    return cands[0] if cands else None
def split_args(s):
    out=[];depth=0;cur=''
    for ch in s:
        if ch in '([{': depth+=1
        if ch in ')]}': depth-=1
        if ch==',' and depth==0: out.append(cur.strip());cur=''
        else: cur+=ch
    if cur.strip(): out.append(cur.strip())
    return out
def find_calls(txt,idtype):
    res=[]
    names=[idtype]
    if idtype.endswith('Id'): names.append(idtype[:-2]+'ID')
    pat=r'\bNew(%s)\('%'|'.join(re.escape(n) for n in names) if idtype else r'$^'
    for m in re.finditer(pat,txt):
        i=m.end();depth=1;j=i
        while j<len(txt) and depth>0:
            if txt[j]=='(':depth+=1
            elif txt[j]==')':depth-=1
            j+=1
        res.append(split_args(txt[i:j-1]))
    return res
def classify(arg):
    a=arg
    if re.fullmatch(r'"[^"]*"',a): return 'constant',a
    if re.search(r'\w+\.(RecordType|ChannelType|Service|Type|Kind|Name)\w*$',a) and not re.search(r'\b(d|model|config|state|id|\w+Id|\w+ID)\.',a): return 'constant',a
    if re.search(r'd\.Get\("([a-z_0-9]+)"\)',a): return 'argument',re.search(r'd\.Get\("([a-z_0-9]+)"\)',a).group(1)
    if re.search(r'\b(model|config|state|plan|input)\.\w+',a): return 'argument',a
    if re.search(r'(resp|result|response|read|existing|org|run|props|future)\b|\.Model\b|\*\w+\.ID|\.Id\b',a,re.I): return 'response?',a
    return 'other',a
out=collections.defaultdict(list)
for x in rows:
    t=x['type']; f=resfile(t)
    src=x.get('source') or {}
    idt=src.get('id_type','')
    calls=find_calls(texts[f],idt) if f else []
    if not calls:
        out[(x['reason'],'no-call')].append((t,idt,os.path.relpath(f,SRC) if f else '-'))
        continue
    lastargs=[c[-1] for c in calls if c]
    kinds=[classify(a) for a in lastargs]
    k=kinds[0]
    out[(x['reason'],k[0])].append((t,k[1],' || '.join(', '.join(c) for c in calls[:1])[:200]))
for (r,k),v in sorted(out.items()):
    print('=====',len(v),r,'::',k)
    for e in v: print('   ',' | '.join(e))
