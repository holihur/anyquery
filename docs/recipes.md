# SaaS Recipe 验证清单

这些 recipe 是通用 REST connector 的默认配置，不是“开箱即用”的认证实现。它们节省分页/字段配置时间，但在没有真实凭据验证前仍标记为 `template`。

## 当前状态

| Recipe | 分页 | 认证 | Mock 契约测试 | 真实凭据验证 |
| --- | --- | --- | --- | --- |
| GitHub | Link header | Bearer / PAT | ✅ | ⬜ |
| GitLab | page | Bearer / PAT | ⬜ | ⬜ |
| Jira | offset (`startAt`) | Basic / API token | ✅ | ⬜ |
| Slack | cursor | Bearer | ✅ | ⬜ |
| Notion | POST body | Bearer / integration token | ✅ | ⬜ |
| Airtable | cursor (`offset`) | Bearer / PAT | ⬜ | ⬜ |
| Salesforce | Link (`nextRecordsUrl`) | OAuth2 | ⬜ | ⬜ |
| HubSpot | cursor (`after`) | Bearer / private app | ⬜ | ⬜ |
| Shopify | Link | Admin API token | ⬜ | ⬜ |
| Zendesk | Link | Basic / OAuth | ⬜ | ⬜ |
| Intercom | Link (`pages.next`) | Bearer | ⬜ | ⬜ |
| PagerDuty | offset | API token | ⬜ | ⬜ |
| Asana | cursor (`next_page.offset`) | PAT / OAuth | ⬜ | ⬜ |
| X / Twitter | cursor (`pagination_token`) | Bearer | ⬜ | ⬜ |
| Telegram | offset | Bot token | ⬜ | ⬜ |
| ClickUp | page | API token | ⬜ | ⬜ |

Mock 契约测试覆盖的是**请求形状和分页状态机**，不代表真实 API 的字段、权限或限流已经验证。

## 真实凭据验证步骤

对每个 recipe：

1. 使用**只读**凭据，不要用生产管理员 token。
2. 在 Data sources 中创建对应类型的数据源，填写真实 endpoint、认证 header 或 OAuth2 字段。
3. 点击 **Test**，确认 `Ping` 通过。
4. 打开 Query Studio，确认 Schema 中的表和列符合预期。
5. 执行：

   ```sql
   SELECT * FROM source.table LIMIT 10;
   ```

6. 把 LIMIT 提高到超过一页，例如 500，确认：
   - 行数连续，没有重复
   - 没有静默缺页
   - `truncated` 只在达到配置上限时出现
7. 检查错误路径：
   - 错误 token 返回明确的 401/403
   - 分页 cursor 失效时不会无限循环
8. 在 PR 中更新 `docs/data-sources-top100.md` 的成熟度和 `docs/recipes.md` 的验证状态，并附上不含凭据的请求/字段样例。

## 成熟度规则

- `template`：配置模板，仅 mock 契约通过或尚未验证
- `compatible`：协议兼容且已知字段映射，但未完成真实账号验证
- `native`：独立驱动/适配器，真实 endpoint 完成连接、Schema、分页和查询验证

不要把“CI mock 通过”直接标成 `native`。
