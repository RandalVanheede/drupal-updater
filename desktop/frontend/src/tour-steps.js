// First-run tour: coach marks over real elements (data-tour attributes and classes in App.svelte).
export const STEPS = [
  { view: 'projects', sel: '[data-tour=projects]', title: 'Your projects', text: 'Drupal projects found in your folders, grouped by what needs doing: security updates first, then other updates. Up to date projects are folded away; click a group to open or close it.' },
  { view: 'projects', sel: '.project-list li', title: 'One project', text: 'Each row shows the Drupal core version and the version it will move to. SLA marks a site with a maintenance contract, and the number of modules comes from the applications dashboard. The i button opens the details: folder, customer, every pending module.' },
  { view: 'projects', sel: '.group-select', title: 'Select a whole group', text: 'Select all ticks every project in that group, for example all sites with a security update.' },
  { view: 'projects', sel: '[data-tour=filters]', title: 'Narrow it down', text: 'Filter by name, or show only projects with an SLA. Versions are read from each repository once a day; Refresh fetches them all now.' },
  { view: 'projects', sel: '[data-tour=mode]', title: 'Update mode', text: 'Standard applies all minor and patch updates. Disaster only applies security fixes.' },
  { view: 'projects', sel: '[data-tour=start]', title: 'Run the update', text: 'Claude updates each selected project on its own branch, several at a time. Follow them on the Running tab; when one finishes you review it, confirm the branch and can open a merge request.' },
  { view: 'config', tab: 'projects', sel: '[data-tour=folders]', title: 'Where projects come from', text: 'Add the folders to scan, exclude projects, and choose how many run at once. Nothing shows up on the Projects tab until a folder is added.' },
  { view: 'config', tab: 'claude', sel: '[data-tour=claude]', openAdvanced: true, title: 'Claude settings', text: 'Model, permission mode and timeout, plus the allowed tools and the prompt in the advanced part (opened here). Per-project overrides have their own tab.' },
  { view: 'config', tab: 'dashboard', sel: '[data-tour=dashboard]', title: 'Applications dashboard', text: 'Connect once to see SLAs, customers and pending modules. Projects are matched by name or detected from their repository; fix or confirm a match here.' },
  { view: 'projects', sel: '[data-tour=help]', title: 'That is it', text: 'Click ? any time to see this tour again.' },
];
