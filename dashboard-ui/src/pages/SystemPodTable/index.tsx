import {
  ActionType,
  FooterToolbar,
  PageContainer,
  ProDescriptions,
  ProColumns,
  ProTable,
  ProDescriptionsItemProps,
} from '@ant-design/pro-components';
import { Button, Divider, Drawer, message } from 'antd';
import React, { useRef, useState } from 'react';
import { Pod, listAppPods } from '@/services/pod';
import { Link } from 'umi';

const AppPodTable: React.FC<unknown> = () => {
  const [createModalVisible, handleModalVisible] = useState<boolean>(false);
  const [updateModalVisible, handleUpdateModalVisible] =
    useState<boolean>(false);
  const [stepFormValues, setStepFormValues] = useState({});
  const actionRef = useRef<ActionType>();
  const [selectedRowsState, setSelectedRows] = useState<Pod[]>([]);
  const columns: ProColumns<Pod>[] = [
    {
      title: '名称',
      formItemProps: {
        rules: [
          {
            required: true,
            message: '名称为必填项',
          },
        ],
      },
      render: (_, pod) => (
        <Link to={`/pod/${pod.metadata?.namespace}/${pod.metadata?.name}`}>
          {pod.metadata?.namespace + '/' + pod.metadata?.name}
        </Link>
      ),
    },
    {
      title: '持久卷申领',
      render: (_, pod) => {
        if (!pod.mountPods || pod.mountPods.size == 0) {
          return <span>无</span>
        } else if (pod.mountPods.size == 1) {
          const [firstKey] = pod.mountPods.keys();
          const mountPod = pod.mountPods.get(firstKey);
          return (
            <Link to={`/pod/${mountPod?.metadata?.namespace}/${mountPod?.metadata?.name}`}>
              {firstKey}
            </Link>
          )
        } else {
          const podMap = pod.mountPods?.keys()
          const pairs = Array.from(podMap).map((key) => ({
            key: key,
            pod: pod.mountPods?.get(key),
          }))
          return (
            <ul>
              {pairs.map(({key, pod}) => (
                <li>
                <Link to={`/pod/${pod?.metadata?.namespace}/${pod?.metadata?.name}`}>
                  {key}
                </Link>
                </li>
              ))}
            </ul>
          )
        }
      }
    },
    {
      title: '集群内 IP',
      dataIndex: ['status', 'podIP'],
      valueType: 'text',
    },
    {
      title: '状态',
      dataIndex: ['status', 'phase'],
      hideInForm: true,
      valueEnum: {
        0: { text: '等待中...', status: 'Pending' },
        1: { text: '正在运行', status: 'Running' },
        2: { text: '运行成功', status: 'Succeeded' },
        3: { text: '运行失败', status: 'Failed' },
        4: { text: '未知状态', status: 'Unknown' },
      },
    },
    {
      title: '所在节点',
      dataIndex: ['spec', 'nodeName'],
      valueType: 'text',
    },
  ];
  return (
    <PageContainer
      header={{
        title: '应用 Pod 管理',
      }}
    >
      <ProTable<Pod>
        headerTitle="查询表格"
        actionRef={actionRef}
        rowKey="id"
        search={{
          labelWidth: 120,
        }}
        toolBarRender={() => [
          <Button
            key="1"
            type="primary"
            onClick={() => handleModalVisible(true)}
            hidden={true}
          >
            新建
          </Button>,
        ]}
        request={async (params, sort, filter) => {
          const { data, success } = await listAppPods({
            ...params,
            sort,
            filter,
          });
          return {
            data: data || [],
            success,
          };
        }}
        columns={columns}
        rowSelection={{
          onChange: (_, selectedRows) => setSelectedRows(selectedRows),
        }}
      />
      {selectedRowsState?.length > 0 && (
        <FooterToolbar
          extra={
            <div>
              已选择{' '}
              <a style={{ fontWeight: 600 }}>{selectedRowsState.length}</a>{' '}
              项&nbsp;&nbsp;
            </div>
          }
        >
          <Button
            onClick={async () => {
              setSelectedRows([]);
              actionRef.current?.reloadAndRest?.();
            }}
          >
            批量删除
          </Button>
          <Button type="primary">批量审批</Button>
        </FooterToolbar>
      )}
    </PageContainer>
  );
};

export default AppPodTable;