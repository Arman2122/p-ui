## What this library is

Penhoon is the admin panel for a VPN/proxy server — a real production app, not a
generic component kit. Its 27 components are the panel's own building blocks:
client tables, traffic cells, config blocks, notification settings, form fields.
Designs built from them should read like panel screens — dense, form-heavy, and
data-first — not like marketing pages.

## Wrap every design in `PanelProvider`

`PanelProvider` is required, not optional. It mounts Ant Design's `ConfigProvider`
with the panel's theme and initialises i18n. Without it, components calling
`useTheme` throw outright, translated labels render as raw keys, and the `--ant-*`
custom properties that the shipped CSS references are never injected — so styling
silently degrades to unthemed defaults.

```jsx
const { PanelProvider, SettingListItem, Switch } = window.Penhoon;

<PanelProvider>
  <SettingListItem
    title="Enable subscription"
    description="Expose an aggregated subscription URL for this client."
  >
    <Switch defaultChecked />
  </SettingListItem>
</PanelProvider>
```

Everything was verified in the **light** theme; there is no dark variant here.

## The styling idiom: Ant Design props, never utility classes

This system has **no utility-class vocabulary**. Do not write `bg-*`, `p-*`,
`text-*`, or any invented class name — nothing will resolve.

`window.Penhoon` carries the **full Ant Design 6 export surface** next to the panel
components. Compose with the real primitives and their documented antd props:
`Button`, `Input`, `InputNumber`, `Select`, `Switch`, `Checkbox`, `Radio`, `Table`,
`Form`, `Modal`, `Card`, `Tag`, `Tooltip`, `Popover`, `Alert`, `Tabs`, `Space`,
`Flex`, `Row`, `Col`, `Typography`, `Divider`, `Badge`, `Drawer`, `Segmented`.

- **Layout** — `Space`, `Flex`, `Row`/`Col`. Reach for these before hand-rolled CSS.
- **Colour and spacing** — antd theme tokens via `ConfigProvider`; `theme.useToken()`
  when a value is needed in JS. Inline `style` is fine for genuine one-offs.
- **Text** — `Typography.Title` / `.Text` / `.Paragraph`, with `type="secondary"`
  for the muted grey the panel uses for hints and captions.

The ~74 class names inside the shipped `_ds_bundle.css` (`.client-traffic-cell`,
`.config-block`, `.sparkline-container`, `.setting-list-item`, …) are **private to
the components**. Never author markup against them.

## Forms

`FormField` binds an antd control to react-hook-form. It needs a `FormProvider`
above it and expects an antd `<Form layout="vertical">` wrapper — that wrapper is
what puts labels above inputs, which is how every panel form looks. `useZodForm`,
`FormProvider`, `useForm`, `useFormContext` and `Controller` are all on the global.

```jsx
const { PanelProvider, FormProvider, useZodForm, FormField, Form, Input } = window.Penhoon;

function ClientForm() {
  const methods = useZodForm(schema, { defaultValues: { email: '' } });
  return (
    <PanelProvider>
      <FormProvider {...methods}>
        <Form layout="vertical" style={{ maxWidth: 360 }}>
          <FormField name="email" label="Email" required>
            <Input />
          </FormField>
        </Form>
      </FormProvider>
    </PanelProvider>
  );
}
```

## Other panel helpers on the global

`ThemeProvider` / `useTheme` / `buildAntdThemeConfig` (theme context — several
components call `useTheme` and require a provider above them), and `setDatepicker`
/ `useDatepicker`, which switch date rendering between `'gregorian'` and
`'jalalian'`. `setDatepicker` is module-level state: call it once, above the tree.

`dayjs` is on the global too. `DateTimePicker` and antd's own `DatePicker` take
dayjs values, so build them with `dayjs('2026-12-31 23:59:59')` rather than a
native `Date`.

## Where the truth lives

Read `components/<group>/<Name>/<Name>.prompt.md` before using a component — it
carries real usage from the panel's own stories, with working props and fixtures.
`<Name>.d.ts` is the exact prop contract. `styles.css` and the `_ds_bundle.css` it
imports are the only stylesheets that ship.
