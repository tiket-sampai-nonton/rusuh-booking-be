#!/usr/bin/env python3
"""Regenerate the contract matrix: one row per method x path x status x errorCode (backticked UPPER_SNAKE in response description)."""
import yaml,re,glob,os,sys
root=os.path.join(os.path.dirname(__file__),'../../../../..')
rows=[]
for f in sorted(glob.glob(root+'/docs/contracts/openapi/*.yaml')):
    d=yaml.safe_load(open(f)); name=os.path.basename(f)
    for p,ops in d['paths'].items():
        for m,op in ops.items():
            if m not in('get','post','put','delete','patch'):continue
            for st,r in op.get('responses',{}).items():
                desc=r.get('description','') or ''
                codes=sorted(set(re.findall(r'`([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*)`',desc)))
                for c in (codes or ['']):
                    rows.append((name,m.upper(),p,str(st),c,desc.replace('\t',' ').replace('\n',' ')))
svc={'booking.yaml':'booking','internal-booking.yaml':'booking','internal-payment.yaml':'payment','notification.yaml':'notification','payment.yaml':'payment','queue.yaml':'queue','user.yaml':'user'}
print('\n'.join('\t'.join((f'R{i+1:03d}',)+r) for i,r in enumerate(rows)))
