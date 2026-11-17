// First-run tour: coach marks over real elements (data-tour attributes in App.svelte).
export const STEPS = [
  { view: 'projects', sel: '[data-tour=projects]', title: 'Your projects', text: 'Drupal projects found in your base folders show up here. Tick the ones you want to update, or use the filter and Toggle All above.' },
  { view: 'projects', sel: '[data-tour=mode]', title: 'Update mode', text: 'Standard applies all minor and patch updates. Disaster only applies security fixes.' },
  { view: 'projects', sel: '[data-tour=start]', title: 'Run the update', text: 'Claude updates each selected project on its own temporary branch, several at a time. When a project finishes you can review it, confirm, then merge it.' },
  { view: 'config', tab: 'projects', sel: '[data-tour=folders]', title: 'Where projects come from', text: 'Add the folders to scan here. Nothing shows up on the Projects tab until you do.' },
  { view: 'config', tab: 'claude', sel: '[data-tour=claude]', openAdvanced: true, title: 'Claude settings', text: 'Model, permission mode and timeout, plus the allowed tools and the prompt in the advanced part (opened here). Per-project overrides are further down.' },
  { view: 'projects', sel: '[data-tour=help]', title: 'That is it', text: 'Click (?) any time to see this tour again.' },
];
