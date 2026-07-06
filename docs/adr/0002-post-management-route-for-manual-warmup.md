# POST Management API route for manual warmup

The Management Center Tab is rendered as a plugin resource page, but plugin resource routes are browser-navigable GET resources. Manual warmup is a user-triggered mutation, so it will be submitted to an authenticated plugin-owned Management API POST route and then redirect back to the tab, avoiding accidental repeat warmups from refreshes or ordinary navigation.
