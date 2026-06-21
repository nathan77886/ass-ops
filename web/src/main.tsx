import React, { useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  ApiOutlined,
  AppstoreOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DashboardOutlined,
  DeploymentUnitOutlined,
  PlayCircleOutlined,
  RobotOutlined,
  SettingOutlined
} from '@ant-design/icons';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  Form,
  Input,
  Layout,
  List,
  Menu,
  Modal,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  message
} from 'antd';
import './styles.css';

const { Header, Sider, Content } = Layout;
const API = import.meta.env.VITE_API_BASE || '';

type AnyRow = Record<string, any>;

function authToken() {
  return localStorage.getItem('assops_token') || '';
}

async function api(path: string, init: RequestInit = {}) {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  const token = authToken();
  if (token) headers.authorization = `Bearer ${token}`;
  const res = await fetch(`${API}${path}`, { ...init, headers: { ...headers, ...(init.headers || {}) } });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [loading, setLoading] = useState(false);
  return (
    <div className="loginPage">
      <div className="loginPanel">
        <Typography.Title level={1}>ASSOPS</Typography.Title>
        <Typography.Paragraph>Operations cockpit for projects, workers, remotes, and AI runtime context.</Typography.Paragraph>
        <Form
          layout="vertical"
          initialValues={{ email: 'admin@assops.local', password: 'admin1234' }}
          onFinish={async (values) => {
            setLoading(true);
            try {
              const data = await api('/api/auth/login', { method: 'POST', body: JSON.stringify(values) });
              localStorage.setItem('assops_token', data.token);
              onLogin();
            } catch (error: any) {
              message.error(error.message);
            } finally {
              setLoading(false);
            }
          }}
        >
          <Form.Item name="email" label="Email" rules={[{ required: true }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item name="password" label="Password" rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading} block>Sign in</Button>
        </Form>
      </div>
    </div>
  );
}

function useLoad<T>(loader: () => Promise<T>, deps: React.DependencyList) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState('');
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    loader().then((next) => alive && setData(next)).catch((err) => alive && setError(err.message));
    return () => { alive = false; };
  }, [...deps, tick]);
  return { data, error, reload: () => setTick((x) => x + 1) };
}

function Dashboard() {
  const ops = useLoad(() => api('/api/operations'), []);
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Dashboard</Typography.Title>
      <div className="metricGrid">
        <Card><Typography.Text type="secondary">Gateway</Typography.Text><Typography.Title level={3}>Online</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Recent operations</Typography.Text><Typography.Title level={3}>{ops.data?.items?.length || 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Runtime</Typography.Text><Typography.Title level={3}>Codex CLI</Typography.Title></Card>
      </div>
      <Operations embedded />
    </Space>
  );
}

function Projects() {
  const projects = useLoad(() => api('/api/projects'), []);
  const [open, setOpen] = useState(false);
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Projects" onCreate={() => setOpen(true)} />
      <Table rowKey="id" dataSource={projects.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Slug', dataIndex: 'slug' },
        { title: 'Description', dataIndex: 'description' },
        { title: 'Created', dataIndex: 'created_at' }
      ]} />
      <CreateModal title="Create project" open={open} setOpen={setOpen} fields={['name', 'slug', 'description']} onSubmit={(v) => api('/api/projects', { method: 'POST', body: JSON.stringify(v) }).then(projects.reload)} />
    </Space>
  );
}

function ProjectDetail() {
  const projects = useLoad(() => api('/api/projects'), []);
  const project = projects.data?.items?.[0];
  const repos = useLoad(() => project ? api(`/api/projects/${project.id}/git-repositories`) : Promise.resolve({ items: [] }), [project?.id]);
  const [repoOpen, setRepoOpen] = useState(false);
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Project Detail</Typography.Title>
      {!project && <Alert type="info" showIcon message="Create a project first." />}
      {project && (
        <>
          <Card title={project.name} extra={<Button onClick={() => api(`/api/projects/${project.id}/context/generate`, { method: 'POST' }).then(() => message.success('Context generated'))}>Generate context</Button>}>
            <Typography.Paragraph>{project.description || 'No description'}</Typography.Paragraph>
          </Card>
          <Toolbar title="Git repositories" onCreate={() => setRepoOpen(true)} />
          <Table rowKey="id" dataSource={repos.data?.items || []} pagination={false} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Key', dataIndex: 'repo_key' },
            { title: 'Default branch', dataIndex: 'default_branch' }
          ]} />
          <CreateModal title="Create repository" open={repoOpen} setOpen={setRepoOpen} fields={['name', 'repo_key', 'description', 'default_branch']} onSubmit={(v) => api(`/api/projects/${project.id}/git-repositories`, { method: 'POST', body: JSON.stringify(v) }).then(repos.reload)} />
        </>
      )}
    </Space>
  );
}

function GitRemotes() {
  const projects = useLoad(() => api('/api/projects'), []);
  const project = projects.data?.items?.[0];
  const repos = useLoad(() => project ? api(`/api/projects/${project.id}/git-repositories`) : Promise.resolve({ items: [] }), [project?.id]);
  const repo = repos.data?.items?.[0];
  const remotes = useLoad(() => repo ? api(`/api/git-repositories/${repo.id}/remotes`) : Promise.resolve({ items: [] }), [repo?.id]);
  const [open, setOpen] = useState(false);
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Git Remotes" onCreate={() => setOpen(true)} />
      <Table<AnyRow> rowKey="id" dataSource={remotes.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Kind', dataIndex: 'kind' },
        { title: 'URLs', render: (_, row) => (row.urls || []).join(', ') },
        { title: 'Actions', render: (_, row) => <Space><Button size="small" onClick={() => api(`/api/git-remotes/${row.id}/sync`, { method: 'POST', body: '{}' }).then(remotes.reload)}>Sync</Button><Button size="small" onClick={() => api(`/api/git-remotes/${row.id}/tag`, { method: 'POST', body: JSON.stringify({ tag: 'v0.1.0' }) }).then(remotes.reload)}>Tag</Button></Space> }
      ]} />
      {!repo && <Alert type="info" showIcon message="Create a project repository before adding remotes." />}
      <CreateModal title="Create remote" open={open} setOpen={setOpen} fields={['name', 'kind', 'urls', 'default_branch']} onSubmit={(v) => api(`/api/git-repositories/${repo.id}/remotes`, { method: 'POST', body: JSON.stringify({ ...v, urls: String(v.urls || '').split(',').map((x) => x.trim()).filter(Boolean) }) }).then(remotes.reload)} />
    </Space>
  );
}

function Operations({ embedded = false }: { embedded?: boolean }) {
  const ops = useLoad(() => api('/api/operations'), []);
  return (
    <Space direction="vertical" size={16} className="full">
      {!embedded && <Typography.Title level={2}>Operations</Typography.Title>}
      <Table<AnyRow> rowKey="id" dataSource={ops.data?.items || []} pagination={{ pageSize: 8 }} columns={[
        { title: 'Type', dataIndex: 'operation_type' },
        { title: 'Title', dataIndex: 'title' },
        { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{row.status}</Tag> },
        { title: 'Created', dataIndex: 'created_at' }
      ]} />
    </Space>
  );
}

function WorkerNodes() {
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Worker Nodes</Typography.Title>
      <Alert showIcon message="Node workers register through /api/worker-nodes/register. Start one with go run ./backend/cmd/node-worker." />
      <Button type="primary" onClick={() => api('/api/worker-nodes/test-job', { method: 'POST', body: JSON.stringify({ message: 'hello node-worker' }) }).then(() => message.success('Echo job queued'))}>Queue echo job</Button>
      <Operations embedded />
    </Space>
  );
}

function AIRuntime() {
  const runtimes = useLoad(() => api('/api/ai-runtimes'), []);
  const [open, setOpen] = useState(false);
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="AI Runtime" onCreate={() => setOpen(true)} />
      <Table<AnyRow> rowKey="id" dataSource={runtimes.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Type', dataIndex: 'runtime_type' },
        { title: 'Binary', dataIndex: 'codex_binary' },
        { title: 'Status', dataIndex: 'status' },
        { title: 'Action', render: (_, row) => <Button size="small" onClick={() => api(`/api/ai-runtimes/${row.id}/verify`, { method: 'POST' }).then(runtimes.reload)}>Verify</Button> }
      ]} />
      <CreateModal title="Create AI runtime" open={open} setOpen={setOpen} fields={['name', 'runtime_type', 'codex_binary', 'model']} onSubmit={(v) => api('/api/ai-runtimes', { method: 'POST', body: JSON.stringify(v) }).then(runtimes.reload)} />
    </Space>
  );
}

function AgentTasks() {
  const projects = useLoad(() => api('/api/projects'), []);
  const project = projects.data?.items?.[0];
  const [open, setOpen] = useState(false);
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Agent Tasks" onCreate={() => setOpen(true)} />
      <Alert showIcon message="Create a task through the modal, then use API endpoints to generate, approve, and execute plan skeletons." />
      <CreateModal title="Create agent task" open={open} setOpen={setOpen} fields={['title', 'prompt']} onSubmit={(v) => api(`/api/projects/${project.id}/agent/tasks`, { method: 'POST', body: JSON.stringify(v) }).then(() => message.success('Task created'))} />
    </Space>
  );
}

function ConfigPage() {
  const projects = useLoad(() => api('/api/projects'), []);
  const project = projects.data?.items?.[0];
  const [sshOpen, setSSHOpen] = useState(false);
  const ssh = useLoad(() => project ? api(`/api/projects/${project.id}/ssh-machines`) : Promise.resolve({ items: [] }), [project?.id]);
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Argo / SSH</Typography.Title>
      <Tabs items={[
        { key: 'ssh', label: 'SSH Machines', children: <><Toolbar title="SSH Machines" onCreate={() => setSSHOpen(true)} /><Table rowKey="id" dataSource={ssh.data?.items || []} pagination={false} columns={[{ title: 'Name', dataIndex: 'name' }, { title: 'Host', dataIndex: 'host' }, { title: 'User', dataIndex: 'username' }]} /></> },
        { key: 'argo', label: 'Argo Apps', children: <Alert showIcon message="Argo connection CRUD API is available; app sync is intentionally adapter-only in MVP." /> }
      ]} />
      <CreateModal title="Create SSH machine" open={sshOpen} setOpen={setSSHOpen} fields={['name', 'host', 'username']} onSubmit={(v) => api(`/api/projects/${project.id}/ssh-machines`, { method: 'POST', body: JSON.stringify(v) }).then(ssh.reload)} />
    </Space>
  );
}

function Toolbar({ title, onCreate }: { title: string; onCreate: () => void }) {
  return <div className="toolbar"><Typography.Title level={2}>{title}</Typography.Title><Button type="primary" onClick={onCreate}>Create</Button></div>;
}

function CreateModal({ title, open, setOpen, fields, onSubmit }: { title: string; open: boolean; setOpen: (v: boolean) => void; fields: string[]; onSubmit: (values: AnyRow) => Promise<any> }) {
  const [form] = Form.useForm();
  return (
    <Modal title={title} open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={(values) => onSubmit(values).then(() => { setOpen(false); form.resetFields(); })}>
        {fields.map((field) => <Form.Item key={field} name={field} label={field.replaceAll('_', ' ')} rules={field === 'name' || field === 'title' ? [{ required: true }] : []}><Input /></Form.Item>)}
      </Form>
    </Modal>
  );
}

function MobileAIHome({ setPage }: { setPage: (p: string) => void }) {
  const items: Array<[string, string, React.ReactNode]> = [
    ['Agent', 'agent', <RobotOutlined />],
    ['Operations', 'operations', <PlayCircleOutlined />],
    ['Runtime', 'ai', <CodeOutlined />],
    ['Projects', 'projects', <AppstoreOutlined />]
  ];
  return <div className="mobileHome"><Typography.Title>ASSOPS AI</Typography.Title><List grid={{ column: 2 }} dataSource={items} renderItem={([label, key, icon]) => <List.Item><Card onClick={() => setPage(key)}><Space direction="vertical" align="center">{icon}<span>{label}</span></Space></Card></List.Item>} /></div>;
}

function App() {
  const [authed, setAuthed] = useState(Boolean(authToken()));
  const [page, setPage] = useState('dashboard');
  const menu = useMemo(() => [
    { key: 'dashboard', icon: <DashboardOutlined />, label: 'Dashboard' },
    { key: 'projects', icon: <AppstoreOutlined />, label: 'Projects' },
    { key: 'detail', icon: <BranchesOutlined />, label: 'Project Detail' },
    { key: 'remotes', icon: <ApiOutlined />, label: 'Git Remotes' },
    { key: 'operations', icon: <PlayCircleOutlined />, label: 'Operations' },
    { key: 'nodes', icon: <CloudServerOutlined />, label: 'Worker Nodes' },
    { key: 'ai', icon: <CodeOutlined />, label: 'AI Runtime' },
    { key: 'agent', icon: <RobotOutlined />, label: 'Agent Task' },
    { key: 'config', icon: <DeploymentUnitOutlined />, label: 'Argo / SSH' }
  ], []);
  if (!authed) return <Login onLogin={() => setAuthed(true)} />;
  const content: Record<string, React.ReactNode> = {
    dashboard: <Dashboard />, projects: <Projects />, detail: <ProjectDetail />, remotes: <GitRemotes />, operations: <Operations />, nodes: <WorkerNodes />, ai: <AIRuntime />, agent: <AgentTasks />, config: <ConfigPage />
  };
  return (
    <ConfigProvider theme={{ token: { borderRadius: 6, colorPrimary: '#1677ff' } }}>
      <MobileAIHome setPage={setPage} />
      <Layout className="appShell">
        <Sider width={240} breakpoint="lg" collapsedWidth={0}><div className="brand"><SettingOutlined /> ASSOPS</div><Menu theme="dark" mode="inline" selectedKeys={[page]} items={menu} onClick={(e) => setPage(e.key)} /></Sider>
        <Layout>
          <Header className="topbar"><Typography.Text strong>MVP Control Plane</Typography.Text><Button onClick={() => { localStorage.removeItem('assops_token'); setAuthed(false); }}>Sign out</Button></Header>
          <Content className="content">{content[page]}</Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
