"""Part 0: GET /health on all 9 endpoints (api 8081-8085, workers 8091-8094) + auth variants on booking."""
import os; os.environ.setdefault('PART', 'p0')
from mlib import *
for svc, port in [('queue', 8081), ('booking', 8082), ('payment', 8083), ('notification', 8084), ('user', 8085), ('queue', 8091), ('booking', 8092), ('payment', 8093), ('notification', 8094)]:
    call('GET', f'http://localhost:{port}', '/health', want=(200, ''), name=f'health :{port} ({svc})', as_svc=svc)
call('GET', 'booking', '/health', headers={'Authorization': 'Bearer garbage'}, want=(401, 'UNAUTHORIZED'), name='health with garbage bearer')
call('GET', 'booking', '/health', headers={'X-Visitor-Token': 'not-a-token'}, want=(401, 'UNAUTHORIZED'), name='health with garbage visitor token')
