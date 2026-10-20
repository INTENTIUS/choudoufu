import json,os,re,sys
SRC='/private/tmp/claude-501/-Users-alex-Documents-checkouts-intentius-choudoufu/e29f3221-b09d-4226-9252-d8925e09dffa/scratchpad/src/azurerm'
d=json.load(open(sys.argv[1]))
want=sys.argv[2] if len(sys.argv)>2 else 'unmapped'
rows=[x for x in d['types'] if x.get('route')==want]
texts={}
for root,_,fs in os.walk(SRC+'/internal'):
    for f in fs:
        if f.endswith('.go') and not f.endswith('_test.go'):
            p=os.path.join(root,f); texts[p]=open(p,errors='replace').read()
funcs={}
for p,t in texts.items():
    for m in re.finditer(r'^func (\w+)\(\)',t,re.M): funcs.setdefault(m.group(1),p)
def resource_files(t):
    fs=[]
    for p,txt in texts.items():
        if p.endswith('registration.go'):
            m=re.search(r'"%s":\s*(\w+)\(\)'%re.escape(t),txt)
            if m and m.group(1) in funcs: fs.insert(0,funcs[m.group(1)])
        elif ('return "%s"'%t) in txt:
            fs.insert(0,p)
    return fs
for x in rows:
    t=x['type']; fs=resource_files(t)
    calls=[]
    for f in fs[:1]:
        txt=texts[f]
        for m in re.finditer(r'(\w+\.)?New\w*I[dD]\w*\(([^()]*(\([^()]*(\([^()]*\))?[^()]*\))?[^()]*)\)',txt):
            s=re.sub(r'\s+',' ',m.group(0))
            if s not in calls: calls.append(s)
        for m in re.finditer(r'SetId\(([^\n]*)\)',txt):
            s='SetId('+m.group(1)[:100]
            if s not in calls: calls.append(s)
    print(t,'|',x.get('reason'),'|',os.path.relpath(fs[0],SRC) if fs else '-', '|', ' ;; '.join(calls[:4]))
